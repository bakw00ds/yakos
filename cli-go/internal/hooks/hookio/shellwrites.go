package hookio

// shellwrites.go — K-170 (b): decode the files a shell command writes, so the
// codex and agy shells are gated by path-allowlist and secret-scan the way the
// Write tool is. The decoder reads STRUCTURE only (quoting, operators,
// redirections, option clusters); it never looks at free text in stderr.
//
// It is a conservative HEURISTIC, not a shell and not a sandbox. Principle:
// what it can decode it reports as a static target; what it recognises as a
// write but cannot resolve (a variable it never saw assigned, a command
// substitution, "~", a script piped to a shell) it reports as a DYNAMIC
// target, which path-allowlist refuses whenever the agent has a policy.
//
// What it catches (each target is reported once):
//
//   - redirections: > >> >| &> &>> <> N> and >&file, also after "cd" and
//     inside ( ... ) groups, pipelines, lists, loops and if/while bodies;
//   - here-documents and here-strings: "cat <<EOF > f" (the target), bodies of
//     unquoted here-documents are scanned for $(...) and backticks, and the body
//     of a here-document fed to a shell or to an interpreter is analysed;
//   - command substitution $(...), `...`, <(...) and >(...), anywhere in a
//     word, recursively (depth and size capped);
//   - tee [-a] FILE...; cp, mv, install, ln (last operand, -t DIR, every
//     source for mv, "install -d" directories); sed -i / --in-place FILE...;
//     perl -i FILE...; dd of=FILE; curl -o/--output FILE; wget -O FILE;
//     find -exec <writer> {} (dynamic) and find -fprint FILE;
//   - sh/bash/zsh/dash/ksh -c SCRIPT and eval ARGS, recursively; a shell that
//     reads its script from stdin with nothing to analyse is dynamic;
//   - wrapper commands (sudo, env, nohup, time, timeout, nice, command, exec,
//     xargs, stdbuf, setsid) and leading VAR=value assignments;
//   - simple static variables ("F=.env; echo x > $F", "export F=.env") and
//     static "cd DIR" (relative targets are joined to it; "cd" to something
//     dynamic makes later relative targets dynamic);
//   - literal brace lists ("> .{env,bak}");
//   - write calls with a literal path in python, node/deno/bun, ruby, perl and
//     php one-liners (-c/-e) and in their here-documents, and a literal shell
//     string handed to os.system, subprocess, exec or system.
//
// What it cannot see (documented, not gated): a script or program that does
// the write ("python3 build.py", "make", "npm run x", "sh script.sh", a binary,
// "source f"); a command word that is itself dynamic ("$EDITOR f"); output
// files chosen by the tool (tar -x, unzip, git checkout/apply/restore, patch,
// rsync, scp, curl -O, wget -P, awk -i inplace); touch, truncate, chmod, rm and
// mkdir; interpreter calls whose path is built at run time are dynamic only when
// the call itself is recognisable; shell features after an unparsable quote;
// and anything that differs between this lexer and the real shell.
// /dev/null, /dev/stdout, /dev/stderr, /dev/stdin, /dev/tty and /dev/fd/N are
// never reported.

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// ShellWrite is one file a shell command is decoded as writing.
type ShellWrite struct {
	// Path is the target as written (made absolute against a static "cd"), or,
	// for a Dynamic write, a sanitised rendering of the unresolved text.
	Path string
	// Dynamic marks a write whose real target cannot be known statically.
	Dynamic bool
}

const (
	// MaxShellCommandBytes bounds the command text analysed.
	MaxShellCommandBytes = 64 << 10
	maxShellDepth        = 6
	maxShellWrites       = 32
	maxShellTokens       = 20000
	maxRawPath           = 200
)

// shellDecodeFault is a test seam: it lets a test prove that a decoder panic is
// reported as a dynamic write.
var shellDecodeFault func()

// DecodeShellWrites returns the files cmd is decoded as writing. startDir is
// the command's working directory when it is known and absolute ("" = the
// project root, the harness's cwd).
func DecodeShellWrites(cmd, startDir string) (out []ShellWrite) {
	defer func() {
		if recover() != nil {
			// A decoder bug must never become "no writes".
			out = []ShellWrite{{Path: "<shell decoder failed on this command>", Dynamic: true}}
		}
	}()
	if shellDecodeFault != nil {
		shellDecodeFault()
	}
	a := &shAnalyzer{seen: map[string]bool{}, vars: map[string]shVar{}, cwd: cleanDir(startDir)}
	if len(cmd) > MaxShellCommandBytes {
		a.add(ShellWrite{Path: "<command too long to analyse>", Dynamic: true})
		return a.out
	}
	a.script(cmd, 0)
	return a.out
}

type shVar struct {
	val string
	ok  bool // false: assigned a value that is not static
}

type shAnalyzer struct {
	out    []ShellWrite
	seen   map[string]bool
	vars   map[string]shVar
	cwd    string // "" = project root
	cwdDyn bool
	tokens int

	// Conservative tracking of the shell's state (see cd and assign): only an
	// unconditional first command of a && chain may change the directory or
	// define a variable the decoder will trust.
	chainAnd   bool // every operator since the statement began is &&
	chainFirst bool // this command is the first of its statement
	lead       bool // led by then/do/else/{ ...: may not run, may repeat
	cdPending  bool // a static cd applies until the statement ends
	evalDepth  int  // inside eval: its effects reach the caller's shell
	noVars     bool // arithmetic or let: no variable is trusted any more
	wrapShift  bool // the current command runs in a directory env -C / sudo -D chose
}

func cleanDir(d string) string {
	if d == "" || d == "." {
		return ""
	}
	return strings.TrimRight(d, "/")
}

func (a *shAnalyzer) add(w ShellWrite) {
	if w.Dynamic {
		w.Path = sanitizeRaw(w.Path)
	}
	key := w.Path
	if w.Dynamic {
		key = "\x00dyn:" + key
	}
	if a.seen[key] {
		return
	}
	if len(a.out) >= maxShellWrites {
		if !a.seen["\x00overflow"] {
			a.seen["\x00overflow"] = true
			a.out = append(a.out, ShellWrite{Path: "<too many write targets to analyse>", Dynamic: true})
		}
		return
	}
	a.seen[key] = true
	a.out = append(a.out, w)
}

func sanitizeRaw(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) {
			r = '?'
		}
		b.WriteRune(r)
		if b.Len() >= maxRawPath {
			break
		}
	}
	if b.Len() == 0 {
		return "<empty>"
	}
	return b.String()
}

// ---- lexer ------------------------------------------------------------------

type partKind int

const (
	partLit partKind = iota
	partVar          // $NAME or ${NAME}
	partSub          // $(...), `...`, <(...), >(...), $((...))
	partDyn          // an expansion that cannot be resolved ($?, ${X:-y}, "~")
)

type shPart struct {
	kind   partKind
	text   string // literal text; variable name; substitution body; raw dyn text
	quoted bool
}

type shWord struct{ parts []shPart }

type shHeredoc struct {
	delim  string
	expand bool
	strip  bool
	body   string
}

type tokKind int

const (
	tokWord tokKind = iota
	tokOp           // ; & && || | |& newline ( )
	tokRedir
)

type shTok struct {
	kind tokKind
	op   string
	word *shWord
	hd   *shHeredoc
}

type shLexer struct {
	inTest  bool // inside [[ ... ]]: < and > compare, they do not redirect
	s       string
	i       int
	toks    []shTok
	pending []*shHeredoc
	steps   *int
}

func (a *shAnalyzer) lex(src string) []shTok {
	l := &shLexer{s: src, steps: &a.tokens}
	l.run()
	if l.i < len(src) {
		// The step cap stopped the lexer: the rest is unread, so it could hold a
		// write. Say so instead of silently allowing it.
		a.add(ShellWrite{Path: "<command too complex to analyse>", Dynamic: true})
	}
	return l.toks
}

func isWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

func (l *shLexer) run() {
	s := l.s
	for l.i < len(s) && *l.steps < maxShellTokens {
		c := s[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.i++
		case c == '\n':
			l.toks = append(l.toks, shTok{kind: tokOp, op: "\n"})
			l.i++
			l.readHeredocBodies()
		case c == '\\' && l.i+1 < len(s) && s[l.i+1] == '\n':
			l.i += 2
		case c == '#' && l.atWordStart():
			for l.i < len(s) && s[l.i] != '\n' {
				l.i++
			}
		case c == ';':
			l.toks = append(l.toks, shTok{kind: tokOp, op: ";"})
			l.i++
			for l.i < len(s) && (s[l.i] == ';' || s[l.i] == '&') {
				l.i++
			}
		case c == '&':
			switch {
			case strings.HasPrefix(s[l.i:], "&&"):
				l.toks = append(l.toks, shTok{kind: tokOp, op: "&&"})
				l.i += 2
			case strings.HasPrefix(s[l.i:], "&>>"):
				l.toks = append(l.toks, shTok{kind: tokRedir, op: "&>>"})
				l.i += 3
				l.skipZshForce()
			case strings.HasPrefix(s[l.i:], "&>"):
				l.toks = append(l.toks, shTok{kind: tokRedir, op: "&>"})
				l.i += 2
				l.skipZshForce()
			default:
				l.toks = append(l.toks, shTok{kind: tokOp, op: "&"})
				l.i++
			}
		case c == '|':
			switch {
			case strings.HasPrefix(s[l.i:], "||"):
				l.toks = append(l.toks, shTok{kind: tokOp, op: "||"})
				l.i += 2
			case strings.HasPrefix(s[l.i:], "|&"):
				l.toks = append(l.toks, shTok{kind: tokOp, op: "|"})
				l.i += 2
			default:
				l.toks = append(l.toks, shTok{kind: tokOp, op: "|"})
				l.i++
			}
		case c == '(':
			l.toks = append(l.toks, shTok{kind: tokOp, op: "("})
			l.i++
		case c == ')':
			l.toks = append(l.toks, shTok{kind: tokOp, op: ")"})
			l.i++
		case (c == '<' || c == '>') && l.i+1 < len(s) && s[l.i+1] == '(':
			l.readWord()
		case (c == '<' || c == '>') && l.inTest:
			l.toks = append(l.toks, shTok{kind: tokWord, word: &shWord{parts: []shPart{{kind: partLit, text: string(c), quoted: true}}}})
			l.i++
		case c == '>' || c == '<':
			l.readRedir()
		default:
			l.readWord()
		}
		*l.steps++
	}
}

// atWordStart reports whether a '#' here starts a comment.
func (l *shLexer) atWordStart() bool {
	if l.i == 0 {
		return true
	}
	return isWordBreak(l.s[l.i-1])
}

func (l *shLexer) readRedir() {
	s := l.s
	rest := s[l.i:]
	op := ""
	for _, cand := range []string{"<<<", "<<-", "<<", "<>", "<&", ">>", ">|", ">&", ">", "<"} {
		if strings.HasPrefix(rest, cand) {
			op = cand
			break
		}
	}
	l.i += len(op)
	if strings.HasPrefix(op, ">") && l.i < len(s) && s[l.i] == '!' {
		l.i++ // zsh: >! >>! >&! force the redirect over noclobber
	}
	t := shTok{kind: tokRedir, op: op}
	if op == "<<" || op == "<<-" {
		l.skipBlanks()
		w := l.scanWord()
		hd := &shHeredoc{strip: op == "<<-", expand: true}
		if w != nil {
			var b strings.Builder
			for _, p := range w.parts {
				if p.quoted {
					hd.expand = false
				}
				b.WriteString(p.text)
			}
			hd.delim = b.String()
		}
		t.hd = hd
		l.pending = append(l.pending, hd)
	}
	l.toks = append(l.toks, t)
}

// skipZshForce consumes the "!" or "|" of zsh's &>! and &>| forms.
func (l *shLexer) skipZshForce() {
	if l.i < len(l.s) && (l.s[l.i] == '!' || l.s[l.i] == '|') {
		l.i++
	}
}

func (l *shLexer) skipBlanks() {
	for l.i < len(l.s) && (l.s[l.i] == ' ' || l.s[l.i] == '\t') {
		l.i++
	}
}

// readHeredocBodies consumes the bodies of the here-documents opened on the
// line that just ended.
func (l *shLexer) readHeredocBodies() {
	for _, hd := range l.pending {
		var body strings.Builder
		for l.i < len(l.s) {
			end := strings.IndexByte(l.s[l.i:], '\n')
			var line string
			if end < 0 {
				line, l.i = l.s[l.i:], len(l.s)
			} else {
				line, l.i = l.s[l.i:l.i+end], l.i+end+1
			}
			cmp := strings.TrimRight(line, "\r")
			if hd.strip {
				cmp = strings.TrimLeft(cmp, "\t")
			}
			if cmp == hd.delim {
				break
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		hd.body = body.String()
	}
	l.pending = nil
}

func (l *shLexer) readWord() {
	if w := l.scanWord(); w != nil {
		if len(w.parts) == 1 && w.parts[0].kind == partLit && !w.parts[0].quoted {
			switch w.parts[0].text {
			case "[[":
				if n := len(l.toks); n == 0 || l.toks[n-1].kind == tokOp {
					l.inTest = true
				}
			case "]]":
				l.inTest = false
			}
		}
		l.toks = append(l.toks, shTok{kind: tokWord, word: w})
	}
}

// scanWord reads one word starting at l.i (nil when nothing was read).
func (l *shLexer) scanWord() *shWord {
	s := l.s
	w := &shWord{}
	lit := func(text string, quoted bool) {
		if text == "" {
			return
		}
		if n := len(w.parts); n > 0 && w.parts[n-1].kind == partLit && w.parts[n-1].quoted == quoted {
			w.parts[n-1].text += text
			return
		}
		w.parts = append(w.parts, shPart{kind: partLit, text: text, quoted: quoted})
	}
	start := l.i
	for l.i < len(s) {
		c := s[l.i]
		if (c == '<' || c == '>') && l.i+1 < len(s) && s[l.i+1] == '(' {
			body, n := balanced(s[l.i+2:], '(', ')')
			w.parts = append(w.parts, shPart{kind: partSub, text: body})
			l.i += 2 + n
			continue
		}
		if c == '(' && len(w.parts) > 0 && l.i > start {
			// word immediately followed by "(": a zsh glob qualifier or
			// alternation (.en(v|x)), "=(cmd)" or an array value. Not static.
			body, n := balanced(s[l.i+1:], '(', ')')
			if s[l.i-1] == '=' {
				w.parts = append(w.parts, shPart{kind: partSub, text: body})
			}
			w.parts = append(w.parts, shPart{kind: partDyn, text: "(" + body + ")"})
			addSubs(w, body, false)
			l.i += 1 + n
			continue
		}
		if isWordBreak(c) {
			break
		}
		switch c {
		case '\\':
			if l.i+1 < len(s) {
				if s[l.i+1] == '\n' {
					l.i += 2
					continue
				}
				lit(string(s[l.i+1]), true)
				l.i += 2
			} else {
				l.i++
			}
		case '\'':
			end := strings.IndexByte(s[l.i+1:], '\'')
			if end < 0 {
				lit(s[l.i+1:], true)
				l.i = len(s)
			} else {
				lit(s[l.i+1:l.i+1+end], true)
				l.i += end + 2
			}
		case '"':
			l.i++
			l.scanDouble(w, lit)
		case '`':
			body, n := backtick(s[l.i+1:])
			w.parts = append(w.parts, shPart{kind: partSub, text: body})
			l.i += 1 + n
		case '$':
			l.scanDollar(w, lit, false)
		case '~':
			if l.i == start {
				// a leading ~ expands to a home directory: not static
				w.parts = append(w.parts, shPart{kind: partDyn, text: "~"})
				l.i++
			} else {
				lit("~", false)
				l.i++
			}
		default:
			j := l.i
			for j < len(s) && !isWordBreak(s[j]) && !strings.ContainsRune("\\'\"`$", rune(s[j])) {
				j++
			}
			if j == l.i {
				j++
			}
			lit(s[l.i:j], false)
			l.i = j
		}
	}
	if len(w.parts) == 0 {
		if l.i == start {
			l.i++ // never loop without progress
		}
		// an empty quoted word ("" or '') is still a word
		if l.i-start >= 2 {
			return &shWord{parts: []shPart{{kind: partLit, text: "", quoted: true}}}
		}
		return nil
	}
	return w
}

// scanDouble reads a double-quoted run (l.i is just past the opening quote).
func (l *shLexer) scanDouble(w *shWord, lit func(string, bool)) {
	s := l.s
	for l.i < len(s) {
		c := s[l.i]
		switch c {
		case '"':
			l.i++
			if len(w.parts) == 0 {
				w.parts = append(w.parts, shPart{kind: partLit, text: "", quoted: true})
			}
			return
		case '\\':
			if l.i+1 < len(s) {
				n := s[l.i+1]
				switch n {
				case '"', '\\', '$', '`':
					lit(string(n), true)
				case '\n':
				default:
					lit("\\"+string(n), true)
				}
				l.i += 2
			} else {
				l.i++
			}
		case '`':
			body, n := backtick(s[l.i+1:])
			w.parts = append(w.parts, shPart{kind: partSub, text: body, quoted: true})
			l.i += 1 + n
		case '$':
			l.scanDollar(w, lit, true)
		default:
			j := l.i
			for j < len(s) && !strings.ContainsRune("\"\\`$", rune(s[j])) {
				j++
			}
			lit(s[l.i:j], true)
			l.i = j
		}
	}
}

func isNameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isNameChar(c byte) bool  { return isNameStart(c) || c >= '0' && c <= '9' }

// scanDollar handles a '$' at l.i.
func (l *shLexer) scanDollar(w *shWord, lit func(string, bool), quoted bool) {
	s := l.s
	if l.i+1 >= len(s) {
		lit("$", quoted)
		l.i++
		return
	}
	n := s[l.i+1]
	switch {
	case n == '(':
		body, k := balanced(s[l.i+2:], '(', ')')
		if strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")") {
			// $(( ... )) is arithmetic, not a command: its > and < compare. But
			// a substitution inside it still runs.
			w.parts = append(w.parts, shPart{kind: partDyn, text: "$(" + body + ")", quoted: quoted})
			addSubs(w, body, quoted)
		} else {
			w.parts = append(w.parts, shPart{kind: partSub, text: body, quoted: quoted})
		}
		l.i += 2 + k
	case n == '{':
		body, k := balanced(s[l.i+2:], '{', '}')
		kind := partDyn
		if isSimpleName(body) {
			kind = partVar
		}
		w.parts = append(w.parts, shPart{kind: kind, text: body, quoted: quoted})
		addSubs(w, body, quoted) // ${F:-$(cmd)} runs cmd
		l.i += 2 + k
	case n == '[':
		body, k := balanced(s[l.i+2:], '[', ']') // old arithmetic: $[ ... ]
		w.parts = append(w.parts, shPart{kind: partDyn, text: "$[" + body + "]", quoted: quoted})
		addSubs(w, body, quoted)
		l.i += 2 + k
	case n == '"' && !quoted:
		// $"..." is a locale-translated string: the text may change at run
		// time. Dynamic; the quoted string itself is lexed next.
		w.parts = append(w.parts, shPart{kind: partDyn, text: "$\"", quoted: false})
		l.i++
	case n == '\'' && !quoted:
		end := l.i + 2
		for end < len(s) && s[end] != '\'' {
			if s[end] == '\\' && end+1 < len(s) {
				end++
			}
			end++
		}
		raw := s[l.i+2 : min(end, len(s))]
		if v, ok := ansiC(raw); ok {
			lit(v, true)
		} else {
			w.parts = append(w.parts, shPart{kind: partDyn, text: "$'" + raw + "'", quoted: true})
		}
		l.i = end + 1
	case isNameStart(n):
		j := l.i + 1
		for j < len(s) && isNameChar(s[j]) {
			j++
		}
		w.parts = append(w.parts, shPart{kind: partVar, text: s[l.i+1 : j], quoted: quoted})
		l.i = j
	case strings.ContainsRune("?$!#@*-0123456789", rune(n)):
		w.parts = append(w.parts, shPart{kind: partDyn, text: "$" + string(n), quoted: quoted})
		l.i += 2
	default:
		lit("$", quoted)
		l.i++
	}
}

// addSubs appends one substitution part for every command substitution that
// text contains.
func addSubs(w *shWord, text string, quoted bool) {
	for _, inner := range substitutionsIn(text) {
		w.parts = append(w.parts, shPart{kind: partSub, text: inner, quoted: quoted})
	}
}

// ansiC decodes the body of $'...'. An escape it does not decode exactly makes
// the whole word dynamic (ok false): a wrong static path would be worse than an
// unknown one.
func ansiC(raw string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(raw) {
			return "", false
		}
		switch e := raw[i]; e {
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'e', 'E':
			b.WriteByte(27)
		case 'f':
			b.WriteByte(12)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte(11)
		case '\\', '\'', '"', '?':
			b.WriteByte(e)
		case 'x':
			n := 0
			for n < 2 && i+1+n < len(raw) && isHex(raw[i+1+n]) {
				n++
			}
			if n == 0 {
				return "", false
			}
			v, _ := hexByte(raw[i+1 : i+1+n])
			b.WriteByte(v)
			i += n
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v, n := 0, 0
			for n < 3 && i+n < len(raw) && raw[i+n] >= '0' && raw[i+n] <= '7' {
				v = v*8 + int(raw[i+n]-'0')
				n++
			}
			b.WriteByte(byte(v))
			i += n - 1
		case 'u', 'U':
			max := 4
			if e == 'U' {
				max = 8
			}
			n, r := 0, rune(0)
			for n < max && i+1+n < len(raw) && isHex(raw[i+1+n]) {
				v, _ := hexByte("0" + raw[i+1+n:i+2+n])
				r = r<<4 | rune(v)
				n++
			}
			if n == 0 || r > 0x10FFFF {
				return "", false
			}
			b.WriteRune(r)
			i += n
		default:
			return "", false // \cX and anything else
		}
	}
	return b.String(), true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexByte(s string) (byte, bool) {
	var v byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | (c - '0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | (c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | (c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return v, true
}

func isSimpleName(s string) bool {
	if s == "" || !isNameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isNameChar(s[i]) {
			return false
		}
	}
	return true
}

// balanced returns the text up to the bracket matching an already-consumed
// opener and the number of bytes consumed including the closer. Quotes inside
// are honoured.
func balanced(s string, open, closer byte) (string, int) {
	depth := 1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '\'':
			if open == '(' {
				if j := strings.IndexByte(s[i+1:], '\''); j >= 0 {
					i += j + 1
				}
			}
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case open:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return s[:i], i + 1
			}
		}
	}
	return s, len(s)
}

func backtick(s string) (string, int) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				if s[i+1] == '`' || s[i+1] == '\\' || s[i+1] == '$' {
					b.WriteByte(s[i+1])
				} else {
					b.WriteByte('\\')
					b.WriteByte(s[i+1])
				}
				i++
			}
		case '`':
			return b.String(), i + 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), len(s)
}

// ---- analysis ---------------------------------------------------------------

type shState struct {
	cwd       string
	cwdDyn    bool
	cdPending bool
	vars      map[string]shVar
}

func (a *shAnalyzer) save() shState {
	v := make(map[string]shVar, len(a.vars))
	for k, x := range a.vars {
		v[k] = x
	}
	return shState{a.cwd, a.cwdDyn, a.cdPending, v}
}

func (a *shAnalyzer) restore(s shState) {
	a.cwd, a.cwdDyn, a.cdPending, a.vars = s.cwd, s.cwdDyn, s.cdPending, s.vars
}

func (a *shAnalyzer) script(src string, depth int) {
	if depth > maxShellDepth {
		a.add(ShellWrite{Path: "<shell nesting too deep to analyse>", Dynamic: true})
		return
	}
	toks := a.lex(src)
	var stack []shState
	var cmd []shTok
	chainAnd, first := true, true
	flush := func() {
		if len(cmd) > 0 {
			a.chainAnd, a.chainFirst = chainAnd, first
			a.command(cmd, depth)
			cmd = nil
		}
	}
	prevOpen := false
	for _, t := range toks {
		if t.kind == tokOp {
			flush()
			switch t.op {
			case "(":
				a.boundary()
				if prevOpen {
					a.noVars = true // "((": an arithmetic command assigns
				}
				stack = append(stack, a.save())
				chainAnd, first = true, true
				prevOpen = true
				continue
			case ")":
				a.boundary()
				if n := len(stack); n > 0 {
					a.restore(stack[n-1])
					stack = stack[:n-1]
				}
				a.cdPending = false
				chainAnd, first = false, false
			case "&&":
				first = false
			case ";", "\n":
				a.boundary()
				chainAnd, first = true, true
			default: // || | &
				a.boundary()
				chainAnd, first = false, false
			}
			prevOpen = false
			continue
		}
		prevOpen = false
		cmd = append(cmd, t)
	}
	flush()
	a.boundary()
}

// boundary ends a && chain: a cd that applied inside it may have failed, so
// whatever runs next is in an unknown directory.
func (a *shAnalyzer) boundary() {
	if a.cdPending {
		a.cwdDyn = true
		a.cdPending = false
	}
}

// resolved is a word after variable resolution.
type resolved struct {
	val    string
	dyn    bool
	raw    string // unresolved rendering, for a dynamic report
	quoted bool   // wholly quoted (no brace expansion)
}

func (a *shAnalyzer) resolve(w *shWord) resolved {
	var val, raw strings.Builder
	r := resolved{quoted: true}
	for _, p := range w.parts {
		switch p.kind {
		case partLit:
			val.WriteString(p.text)
			raw.WriteString(p.text)
			if !p.quoted {
				r.quoted = false
				if strings.ContainsAny(p.text, "*?[") {
					r.dyn = true // a glob: the shell picks the file(s), not us
				}
			}
		case partVar:
			raw.WriteString("$" + p.text)
			if v, ok := a.vars[p.text]; ok && v.ok && !a.noVars {
				val.WriteString(v.val)
			} else {
				r.dyn = true
			}
		case partSub:
			raw.WriteString("$(" + p.text + ")")
			r.dyn = true
		default:
			raw.WriteString(p.text)
			r.dyn = true
		}
	}
	r.val, r.raw = val.String(), raw.String()
	return r
}

// subs analyses the command substitutions of w (run in a subshell).
func (a *shAnalyzer) subs(w *shWord, depth int) {
	for _, p := range w.parts {
		if p.kind == partSub {
			st := a.save()
			a.script(p.text, depth+1)
			a.restore(st)
		}
	}
}

var assignRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// isAssign reports a leading NAME=value word and its parts after the '='.
func isAssign(w *shWord) (string, *shWord, bool) {
	if len(w.parts) == 0 || w.parts[0].kind != partLit || w.parts[0].quoted {
		return "", nil, false
	}
	m := assignRE.FindString(w.parts[0].text)
	if m == "" {
		return "", nil, false
	}
	rest := &shWord{parts: append([]shPart(nil), w.parts...)}
	rest.parts[0] = shPart{kind: partLit, text: w.parts[0].text[len(m):], quoted: false}
	return m[:len(m)-1], rest, true
}

type shArg struct {
	resolved
	w *shWord
}

func (a *shAnalyzer) command(toks []shTok, depth int) {
	// Nested scripts (substitutions) overwrite the chain context; keep ours.
	chainAnd, chainFirst := a.chainAnd, a.chainFirst
	var words []shArg
	var heredocs []*shHeredoc
	var herestr []string
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokWord:
			a.subs(t.word, depth)
			words = append(words, shArg{a.resolve(t.word), t.word})
		case tokRedir:
			var operand *shWord
			if t.op != "<<" && t.op != "<<-" && i+1 < len(toks) && toks[i+1].kind == tokWord {
				operand = toks[i+1].word
				i++
				a.subs(operand, depth)
			}
			switch t.op {
			case "<<", "<<-":
				heredocs = append(heredocs, t.hd)
				if t.hd.expand {
					a.scanSubsIn(t.hd.body, depth)
				}
			case "<<<":
				if operand != nil {
					herestr = append(herestr, a.resolve(operand).val)
				}
			case ">", ">>", ">|", "&>", "&>>", "<>":
				if operand != nil {
					a.target(a.resolve(operand))
				}
			case ">&":
				if operand != nil {
					r := a.resolve(operand)
					if !isFDWord(r.val) || r.dyn {
						a.target(r)
					}
				}
			}
		}
	}
	a.chainAnd, a.chainFirst = chainAnd, chainFirst
	a.simple(words, heredocs, herestr, depth)
}

func isFDWord(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// substitutionsIn returns the bodies of the $(...) and `...` substitutions in
// text, outermost only (each body is analysed recursively as a script).
func substitutionsIn(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\':
			i++
		case text[i] == '$' && i+1 < len(text) && text[i+1] == '(':
			inner, n := balanced(text[i+2:], '(', ')')
			out = append(out, inner)
			i += 1 + n
		case text[i] == '`':
			inner, n := backtick(text[i+1:])
			out = append(out, inner)
			i += n
		}
	}
	return out
}

// scanSubsIn analyses the substitutions inside an unquoted here-document body.
func (a *shAnalyzer) scanSubsIn(body string, depth int) {
	for _, inner := range substitutionsIn(body) {
		st := a.save()
		a.script(inner, depth+1)
		a.restore(st)
	}
}

// target records a write to the resolved word r (brace lists expanded).
func (a *shAnalyzer) target(r resolved) {
	if r.dyn {
		a.add(ShellWrite{Path: r.raw, Dynamic: true})
		return
	}
	cands := []string{r.val}
	if !r.quoted {
		if ex, ok := braceExpand(r.val); ok {
			cands = ex
		} else if strings.ContainsAny(r.val, "{}") && strings.Contains(r.val, "{") && strings.Contains(r.val, "..") {
			a.add(ShellWrite{Path: r.val, Dynamic: true})
			return
		}
	}
	for _, c := range cands {
		a.addPath(c)
	}
}

func (a *shAnalyzer) addPath(p string) {
	if p == "" {
		return
	}
	switch p {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/tty", "/dev/zero":
		return
	}
	if strings.HasPrefix(p, "/dev/fd/") {
		return
	}
	if !strings.HasPrefix(p, "/") && a.cwd != "" {
		p = a.cwd + "/" + p
	}
	if !strings.HasPrefix(p, "/") && a.cwdDyn {
		a.add(ShellWrite{Path: p, Dynamic: true})
		return
	}
	a.add(ShellWrite{Path: p})
}

// braceExpand expands one level-at-a-time literal comma lists ("a{b,c}d"),
// capped. ok is false when the word has no expandable list.
func braceExpand(s string) ([]string, bool) {
	open := strings.IndexByte(s, '{')
	if open < 0 {
		return nil, false
	}
	depth, closeAt := 0, -1
	var commas []int
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				closeAt = i
			}
		case ',':
			if depth == 1 {
				commas = append(commas, i)
			}
		}
		if closeAt >= 0 {
			break
		}
	}
	if closeAt < 0 || len(commas) == 0 {
		return nil, false
	}
	pre, post := s[:open], s[closeAt+1:]
	var alts []string
	prev := open + 1
	for _, c := range append(commas, closeAt) {
		alts = append(alts, s[prev:c])
		prev = c + 1
	}
	var out []string
	for _, alt := range alts {
		cand := pre + alt + post
		if ex, ok := braceExpand(cand); ok {
			out = append(out, ex...)
		} else {
			out = append(out, cand)
		}
		if len(out) > 64 {
			return out[:64], true
		}
	}
	return out, true
}

var wrapperArgs = map[string]string{
	// wrapper -> short/long options that consume the next word
	"sudo":       "u g h p C r t U D R T",
	"doas":       "u C",
	"env":        "u C S",
	"nice":       "n",
	"ionice":     "c n p",
	"timeout":    "s k",
	"stdbuf":     "i o e",
	"xargs":      "n I P d L s E a l i",
	"nohup":      "",
	"time":       "f o",
	"command":    "",
	"builtin":    "",
	"exec":       "a",
	"setsid":     "",
	"caffeinate": "t w",
}

var shells = map[string]bool{"fish": true, "sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "ash": true, "mksh": true}

func baseName(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

var reservedLead = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "while": true, "until": true,
	"do": true, "!": true, "{": true, "}": true, "coproc": true}

var extAssignRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(\[[^\]]*\])?\+?=`)

func (a *shAnalyzer) simple(all []shArg, heredocs []*shHeredoc, herestr []string, depth int) {
	words := all
	// reserved words that lead a command
	a.lead = false
	for len(words) > 0 && !words[0].dyn && reservedLead[words[0].val] {
		if words[0].val != "!" {
			a.lead = true
		}
		words = words[1:]
	}
	// for/select define their loop variable
	if len(words) > 1 && !words[0].dyn && (words[0].val == "for" || words[0].val == "select") && !words[1].dyn && isSimpleName(words[1].val) {
		a.vars[words[1].val] = shVar{ok: false}
	}
	// assignments
	assigns := 0
	for assigns < len(words) {
		w := words[assigns].w
		if _, _, ok := isAssign(w); ok {
			assigns++
			continue
		}
		// NAME+=x and NAME[i]=x: not a plain assignment; the name goes dynamic
		if len(w.parts) > 0 && w.parts[0].kind == partLit && !w.parts[0].quoted {
			if extAssignRE.MatchString(w.parts[0].text) {
				assigns++ // assign() makes the name dynamic
				continue
			}
		}
		break
	}
	if assigns == len(words) {
		for _, w := range words {
			a.assign(w.w)
		}
		return
	}
	words = words[assigns:]
	a.dispatch(words, heredocs, herestr, depth)
}

// assign records NAME=value. Only a plain NAME=literal that is the first
// command of its statement, outside eval and conditional constructs, and the
// first assignment of that name, is trusted; every other form makes the name
// dynamic from then on.
func (a *shAnalyzer) assign(w *shWord) {
	name, rest, ok := isAssign(w)
	if !ok {
		if len(w.parts) > 0 && w.parts[0].kind == partLit {
			if m := extAssignRE.FindStringSubmatch(w.parts[0].text); m != nil {
				a.vars[m[1]] = shVar{ok: false}
			}
		}
		return
	}
	r := a.resolve(rest)
	_, seen := a.vars[name]
	if r.dyn || seen || !a.chainFirst || a.lead || a.evalDepth > 0 {
		a.vars[name] = shVar{ok: false}
		return
	}
	a.vars[name] = shVar{val: r.val, ok: true}
}

// dynVars makes every plain-name operand a dynamic variable (read, mapfile...).
func (a *shAnalyzer) dynVars(args []shArg) {
	for _, x := range args {
		if !x.dyn && isSimpleName(x.val) {
			a.vars[x.val] = shVar{ok: false}
		}
	}
}

// stripWrappers peels sudo/env/nohup/... and returns the remaining words.
func (a *shAnalyzer) stripWrappers(words []shArg) []shArg {
	for len(words) > 0 {
		w := words[0]
		if w.dyn {
			return words
		}
		name := baseName(w.val)
		spec, isWrap := wrapperArgs[name]
		if !isWrap {
			return words
		}
		consume := map[string]bool{}
		for _, f := range strings.Fields(spec) {
			consume[f] = true
		}
		words = words[1:]
		for len(words) > 0 {
			x := words[0]
			if x.dyn {
				break
			}
			switch {
			case name == "env" && assignRE.MatchString(x.val):
				words = words[1:]
			case x.val == "--":
				words = words[1:]
				goto next
			case strings.HasPrefix(x.val, "--"):
				if (name == "env" || name == "sudo") && strings.HasPrefix(x.val, "--chdir") {
					a.wrapShift = true
				}
				words = words[1:]
				if !strings.Contains(x.val, "=") && len(words) > 0 && consume[strings.TrimPrefix(x.val, "--")] {
					words = words[1:]
				}
			case strings.HasPrefix(x.val, "-") && len(x.val) > 1:
				words = words[1:]
				last := x.val[len(x.val)-1:]
				if (name == "env" && last == "C") || (name == "sudo" && last == "D") {
					a.wrapShift = true
				}
				if consume[last] && len(words) > 0 {
					words = words[1:]
				}
			case name == "timeout" && len(words) > 0 && looksDuration(x.val):
				words = words[1:]
				goto next
			default:
				goto next
			}
		}
	next:
	}
	return words
}

func looksDuration(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c == '.' || (i == len(s)-1 && strings.ContainsRune("smhd", rune(c)))) {
			return false
		}
	}
	return true
}

func (a *shAnalyzer) dispatch(words []shArg, heredocs []*shHeredoc, herestr []string, depth int) {
	a.wrapShift = false
	words = a.stripWrappers(words)
	if a.wrapShift {
		// env -C / sudo -D: the command runs in a directory we cannot track.
		saved := a.cwdDyn
		a.cwdDyn = true
		defer func() { a.cwdDyn = saved }()
		a.wrapShift = false
	}
	if len(words) == 0 {
		return
	}
	if words[0].dyn {
		return // a dynamic command word: not decodable (documented)
	}
	name := baseName(words[0].val)
	args := words[1:]
	switch {
	case name == "cd":
		a.cd(args)
	case name == "pushd" || name == "popd":
		a.cwdDyn = true
	case name == "export" || name == "declare" || name == "local" || name == "readonly" || name == "typeset":
		for _, x := range args {
			if !x.dyn && (x.val == "-n" || x.val == "+n") {
				a.noVars = true // a nameref: any name may now alias another
			}
			a.assign(x.w)
		}
	case name == "read" || name == "mapfile" || name == "readarray" || name == "getopts" || name == "unset":
		a.dynVars(args)
	case name == "let":
		a.noVars = true
	case name == "printf":
		for i, x := range args {
			if !x.dyn && x.val == "-v" && i+1 < len(args) {
				a.dynVars(args[i+1 : i+2])
			}
		}
	case shells[name]:
		a.shell(args, heredocs, herestr, depth)
	case name == "eval":
		a.evalArgs(args, depth)
	case name == "tee":
		for _, f := range operands(args, "", "") {
			a.target(f)
		}
	case name == "cp" || name == "mv" || name == "install" || name == "ln":
		a.copyLike(name, args)
	case name == "sed":
		a.sed(args)
	case name == "perl" && hasInplace(args):
		a.perlInplace(args)
		a.interpreter(name, args, heredocs, herestr, depth)
	case name == "dd":
		for _, x := range args {
			if v, ok := strings.CutPrefix(x.val, "of="); ok {
				a.target(resolved{val: v, dyn: x.dyn, raw: strings.TrimPrefix(x.raw, "of="), quoted: x.quoted})
			}
		}
	case name == "curl":
		a.optTarget(args, "o", "output")
	case name == "wget":
		a.optTarget(args, "O", "output-document")
	case name == "find":
		a.find(args, depth)
	case name == "rm" || name == "unlink" || name == "shred" || name == "truncate" || name == "touch" || name == "sponge" || name == "gofmt" || name == "goimports" || name == "prettier":
		a.operandTargets(name, args)
	case name == "uniq":
		_, ops, _ := splitOpts(args, "f s w", "skip-fields skip-chars check-chars")
		if len(ops) >= 2 {
			a.target(ops[1])
		}
	case name == "xxd":
		_, ops, _ := splitOpts(args, "c g l s o", "cols groupsize len seek")
		if len(ops) >= 2 {
			a.target(ops[1])
		}
	case name == "ditto":
		a.copyLike("cp", args)
	case name == "yq":
		opts, ops, _ := splitOpts(args, "", "")
		if _, ok := hasOpt(opts, "i", "inplace"); ok && len(ops) > 1 {
			for _, f := range ops[1:] {
				a.target(f)
			}
		}
	case name == "sort":
		opts, _, _ := splitOpts(args, "o t k T S", "output field-separator key temporary-directory buffer-size")
		for _, o := range opts {
			if (o.name == "o" || o.name == "output") && o.has {
				a.target(o.val)
			}
		}
	case name == "awk" || name == "gawk" || name == "mawk" || name == "nawk":
		a.awk(args, depth)
	case interpreterKind(name) != "":
		a.interpreter(name, args, heredocs, herestr, depth)
	}
}

// cd tracks a directory change only when it is certain: a plain cd to a static
// literal that is not led by then/do/else/{, not inside eval, and in a chain of
// && only. It then applies until the statement ends (boundary), because a
// failed cd leaves the shell where it was. Anything else makes later relative
// targets dynamic.
func (a *shAnalyzer) cd(args []shArg) {
	var ops []shArg
	for _, x := range args {
		if !x.dyn && strings.HasPrefix(x.val, "-") && len(x.val) > 1 {
			continue
		}
		ops = append(ops, x)
	}
	if len(ops) == 0 || !a.chainAnd || a.lead || a.evalDepth > 0 {
		a.cwdDyn = true
		return
	}
	d := ops[0]
	if d.dyn || d.val == "-" {
		a.cwdDyn = true
		return
	}
	a.cdPending = true
	if strings.HasPrefix(d.val, "/") {
		a.cwd, a.cwdDyn = cleanDir(d.val), false
		if a.cwd == "" {
			a.cwd = "/"
		}
		return
	}
	if a.cwdDyn {
		return // a relative step from an unknown directory stays unknown
	}
	if a.cwd == "" {
		a.cwd = cleanDir(d.val)
	} else {
		a.cwd = a.cwd + "/" + strings.TrimRight(d.val, "/")
	}
}

// shell analyses sh -c SCRIPT, or stdin scripts.
func (a *shAnalyzer) shell(args []shArg, heredocs []*shHeredoc, herestr []string, depth int) {
	cIdx := -1
	i := 0
	for ; i < len(args); i++ {
		x := args[i]
		if x.dyn || !strings.HasPrefix(x.val, "-") || x.val == "-" {
			break
		}
		if x.val == "--" {
			i++
			break
		}
		if !strings.HasPrefix(x.val, "--") && strings.Contains(x.val[1:], "c") {
			cIdx = i
		}
		// options that take an argument
		if x.val == "-o" || x.val == "+o" || x.val == "-O" || x.val == "--rcfile" || x.val == "--init-file" {
			i++
		}
	}
	if cIdx >= 0 {
		// the script is the first operand after the options
		if i < len(args) {
			s := args[i]
			if s.dyn {
				a.add(ShellWrite{Path: "<shell -c script built at run time: " + s.raw + ">", Dynamic: true})
				return
			}
			st := a.save()
			a.script(s.val, depth+1)
			a.restore(st)
			return
		}
	}
	if i < len(args) {
		return // a script FILE: not decodable (documented)
	}
	// no script operand: the script is stdin
	if len(heredocs) > 0 || len(herestr) > 0 {
		for _, hd := range heredocs {
			st := a.save()
			a.script(hd.body, depth+1)
			a.restore(st)
		}
		for _, s := range herestr {
			st := a.save()
			a.script(s, depth+1)
			a.restore(st)
		}
		return
	}
	a.add(ShellWrite{Path: "<script read from stdin by a shell>", Dynamic: true})
}

func (a *shAnalyzer) evalArgs(args []shArg, depth int) {
	var parts []string
	for _, x := range args {
		if x.dyn {
			a.add(ShellWrite{Path: "<eval of text built at run time: " + x.raw + ">", Dynamic: true})
			return
		}
		parts = append(parts, x.val)
	}
	// eval runs in the caller's shell: its cd and assignments persist, so they
	// are made dynamic (evalDepth) rather than saved and restored.
	a.evalDepth++
	a.script(strings.Join(parts, " "), depth+1)
	a.evalDepth--
	a.cwdDyn = a.cwdDyn || a.cdPending
}

// optSpec splits args into option words and operands for a command whose
// options consume arguments as listed ("e f" = -e and -f take one).
func operands(args []shArg, shortArg, longArg string) []resolved {
	_, ops, _ := splitOpts(args, shortArg, longArg)
	return ops
}

type optHit struct {
	name string // without dashes
	val  resolved
	has  bool // val present
}

// splitOpts walks args like getopt. shortArg/longArg are space-separated
// option names that consume an argument. Options are returned in order, as are
// the operands; "--" ends options. An option cluster "-abc" is expanded to its
// letters, the last of which may take the next word, or the rest of the cluster
// as its argument.
func splitOpts(args []shArg, shortArg, longArg string) ([]optHit, []resolved, bool) {
	sa, la := map[byte]bool{}, map[string]bool{}
	for _, f := range strings.Fields(shortArg) {
		sa[f[0]] = true
	}
	for _, f := range strings.Fields(longArg) {
		la[f] = true
	}
	var opts []optHit
	var ops []resolved
	done := false
	for i := 0; i < len(args); i++ {
		x := args[i]
		if done || x.dyn || !strings.HasPrefix(x.val, "-") || x.val == "-" {
			ops = append(ops, x.resolved)
			continue
		}
		if x.val == "--" {
			done = true
			continue
		}
		if strings.HasPrefix(x.val, "--") {
			body := x.val[2:]
			if k, v, ok := strings.Cut(body, "="); ok {
				opts = append(opts, optHit{name: k, val: resolved{val: v, raw: v, quoted: x.quoted}, has: true})
				continue
			}
			if la[body] && i+1 < len(args) {
				i++
				opts = append(opts, optHit{name: body, val: args[i].resolved, has: true})
				continue
			}
			opts = append(opts, optHit{name: body})
			continue
		}
		cl := x.val[1:]
		for j := 0; j < len(cl); j++ {
			c := cl[j]
			if sa[c] {
				if j+1 < len(cl) {
					opts = append(opts, optHit{name: string(c), val: resolved{val: cl[j+1:], raw: cl[j+1:], quoted: x.quoted}, has: true})
				} else if i+1 < len(args) {
					i++
					opts = append(opts, optHit{name: string(c), val: args[i].resolved, has: true})
				} else {
					opts = append(opts, optHit{name: string(c)})
				}
				break
			}
			opts = append(opts, optHit{name: string(c)})
		}
	}
	return opts, ops, done
}

func hasOpt(opts []optHit, names ...string) (optHit, bool) {
	for _, o := range opts {
		for _, n := range names {
			if o.name == n {
				return o, true
			}
		}
	}
	return optHit{}, false
}

func (a *shAnalyzer) copyLike(name string, args []shArg) {
	shortArg, longArg := "t S", "target-directory suffix"
	switch name {
	case "install":
		shortArg, longArg = "t S m o g", "target-directory suffix mode owner group"
	case "ln":
		shortArg = "t S"
	}
	opts, ops, _ := splitOpts(args, shortArg, longArg)
	if name == "install" {
		if _, d := hasOpt(opts, "d", "directory"); d {
			for _, o := range ops {
				a.target(o)
			}
			return
		}
	}
	var dir *resolved
	if o, ok := hasOpt(opts, "t", "target-directory"); ok && o.has {
		d := o.val
		dir = &d
	}
	if name == "mv" {
		for _, o := range ops {
			a.target(o) // a rename removes the source: a write to it
		}
	}
	if dir != nil {
		for _, src := range ops {
			a.target(joinRes(*dir, baseRes(src)))
		}
		return
	}
	if len(ops) == 0 {
		return
	}
	last := ops[len(ops)-1]
	srcs := ops[:len(ops)-1]
	dirLike := len(ops) > 2 || strings.HasSuffix(last.val, "/")
	if !dirLike {
		a.target(last)
	}
	for _, s := range srcs {
		a.target(joinRes(last, baseRes(s)))
	}
}

func baseRes(r resolved) resolved {
	if r.dyn {
		return r
	}
	b := baseName(strings.TrimRight(r.val, "/"))
	return resolved{val: b, raw: b, quoted: r.quoted}
}

func joinRes(dir, name resolved) resolved {
	if dir.dyn || name.dyn {
		return resolved{val: dir.val + "/" + name.val, dyn: true, raw: dir.raw + "/" + name.raw}
	}
	return resolved{val: strings.TrimRight(dir.val, "/") + "/" + name.val, raw: dir.raw + "/" + name.raw, quoted: dir.quoted && name.quoted}
}

func hasInplace(args []shArg) bool {
	for _, x := range args {
		if x.dyn || !strings.HasPrefix(x.val, "-") {
			continue
		}
		if strings.HasPrefix(x.val, "--") {
			if x.val == "--in-place" || strings.HasPrefix(x.val, "--in-place=") {
				return true
			}
			continue
		}
		if strings.Contains(x.val[1:], "i") {
			return true
		}
	}
	return false
}

func (a *shAnalyzer) sed(args []shArg) {
	if !hasInplace(args) {
		return
	}
	opts, ops, _ := splitOpts(args, "e f l", "expression file line-length")
	// -i takes an optional suffix glued to it; BSD sed takes the NEXT word as
	// the suffix, which splitOpts left as an operand. Drop an empty or
	// dot-leading operand that directly follows the in-place flag.
	scripted := false
	if _, ok := hasOpt(opts, "e", "f", "expression", "file"); ok {
		scripted = true
	}
	if len(ops) > 0 && !ops[0].dyn && (ops[0].val == "" || (strings.HasPrefix(ops[0].val, ".") && !scripted && len(ops) > 2)) {
		ops = ops[1:]
	}
	if !scripted && len(ops) > 0 {
		ops = ops[1:] // the first operand is the script
	}
	for _, f := range ops {
		a.target(f)
	}
}

func (a *shAnalyzer) perlInplace(args []shArg) {
	opts, ops, _ := splitOpts(args, "e E I M m x", "")
	if _, ok := hasOpt(opts, "e", "E"); !ok && len(ops) > 0 {
		ops = ops[1:]
	}
	for _, f := range ops {
		a.target(f)
	}
}

// optTarget records the value of the option (short or long) as a write.
func (a *shAnalyzer) optTarget(args []shArg, short, long string) {
	opts, _, _ := splitOpts(args, short+" "+optStringFor(short), long)
	for _, o := range opts {
		if (o.name == short || o.name == long) && o.has {
			a.target(o.val)
		}
	}
}

func optStringFor(short string) string {
	// curl/wget options that take an argument and could swallow a cluster.
	return "H d u X A e T w x U"
}

func (a *shAnalyzer) find(args []shArg, depth int) {
	for i := 0; i < len(args); i++ {
		x := args[i]
		if x.dyn {
			continue
		}
		switch x.val {
		case "-fprint", "-fprint0", "-fprintf", "-fls":
			if i+1 < len(args) {
				a.target(args[i+1].resolved)
			}
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			var inner []shArg
			for ; j < len(args); j++ {
				if !args[j].dyn && (args[j].val == ";" || args[j].val == "+") {
					break
				}
				w := args[j]
				if !w.dyn && w.val == "{}" {
					w.dyn, w.raw = true, "{}"
				}
				inner = append(inner, w)
			}
			if len(inner) > 0 {
				a.dispatch(inner, nil, nil, depth)
			}
			i = j
		}
	}
}

// shellTools are the tool names, after DecodeShape, whose tool_input holds a
// shell command.
var shellTools = map[string]bool{"Bash": true, "shell": true, "local_shell": true, "exec_command": true, "shell_command": true, "container.exec": true}

// ShellDynamicKey marks a synthesized write whose real target is unknown. It is
// set by ShellWriteInputs only; a forged key on a real Write call can only make
// path-allowlist stricter.
const ShellDynamicKey = "yakos_shell_dynamic"

// ShellWriteInputs returns, for every PreToolUse shell input in ins, one
// synthesized Write input per file DecodeShellWrites finds. Its content is the
// command text, so secret-scan sees a secret that is written with echo. Only
// path-allowlist and secret-scan are given these (see shaperun): a hook that
// counts calls must not count one command several times.
func ShellWriteInputs(ins []hooktype.HookInput) []hooktype.HookInput {
	var out []hooktype.HookInput
	for _, in := range ins {
		if !shellTools[in.Tool] || (in.Event != "" && in.Event != "PreToolUse") {
			continue
		}
		ti := ToolInput(in)
		cmd, dir, readable := shellCommandOf(ti)
		var writes []ShellWrite
		if !readable {
			// A command field we cannot read is not "no command": refuse it
			// under a policy rather than let it through.
			writes = []ShellWrite{{Path: "<shell command in a form that cannot be read>", Dynamic: true}}
		} else if cmd == "" {
			continue
		} else {
			writes = DecodeShellWrites(cmd, dir)
		}
		for _, w := range writes {
			nti := map[string]any{"file_path": w.Path, "content": cmd}
			if w.Dynamic {
				nti[ShellDynamicKey] = true
			}
			p := make(map[string]any, len(in.Payload)+1)
			for k, v := range in.Payload {
				p[k] = v
			}
			p["tool_name"] = "Write"
			p["tool_input"] = nti
			c := in
			c.Tool = "Write"
			c.Payload = p
			out = append(out, c)
		}
	}
	return out
}

// shellCommandOf reads the command (a string, or an argv array rendered as a
// quoted command line) and an absolute working directory from a shell tool's
// tool_input.
func shellCommandOf(ti map[string]any) (cmd, dir string, readable bool) {
	var raw any
	for _, k := range []string{"command", "cmd", "CommandLine"} {
		if v, ok := ti[k]; ok && v != nil {
			raw = v
			break
		}
	}
	switch v := raw.(type) {
	case nil:
		return "", "", true // no command field at all
	case string:
		cmd = v
	case []any:
		var parts []string
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return "", "", false
			}
			parts = append(parts, "'"+strings.ReplaceAll(s, "'", `'\''`)+"'")
		}
		cmd = strings.Join(parts, " ")
	default:
		return "", "", false // an object, number or bool
	}
	for _, k := range []string{"workdir", "cwd", "Cwd", "working_directory"} {
		if s, ok := ti[k].(string); ok && strings.HasPrefix(s, "/") {
			dir = s
			break
		}
	}
	return cmd, dir, true
}

var reAwkRedir = regexp.MustCompile(`>>?\s*("(?:\\.|[^"\\])*")`)
var reAwkSystem = regexp.MustCompile(`\bsystem\s*\(`)

// awk reads the program text for output redirections to a quoted file name
// (print 1 > "f") and system("...") calls. A redirection to anything but a
// quoted literal is indistinguishable from a comparison and is not caught.
func (a *shAnalyzer) awk(args []shArg, depth int) {
	opts, ops, _ := splitOpts(args, "F v f e i", "field-separator assign file source include")
	if _, ok := hasOpt(opts, "f", "file"); ok || len(ops) == 0 {
		return // the program is in a file
	}
	prog := ops[0]
	if prog.dyn {
		return
	}
	for _, m := range reAwkRedir.FindAllStringSubmatch(prog.val, 16) {
		if v, ok := strLit(m[1]); ok {
			a.addPath(v)
		}
	}
	a.callsShell(prog.val, reAwkSystem, depth, false)
}

// operandTargets records the file operands of commands that modify the file
// they are given: rm, unlink, shred, truncate, touch, sponge, and the
// formatters gofmt/goimports/prettier (only with -w / --write).
func (a *shAnalyzer) operandTargets(name string, args []shArg) {
	shortArg, longArg := "", ""
	switch name {
	case "truncate":
		shortArg, longArg = "s r", "size reference"
	case "touch":
		shortArg, longArg = "t d r", "date reference time"
	case "shred":
		shortArg, longArg = "n s", "iterations size"
	}
	opts, ops, _ := splitOpts(args, shortArg, longArg)
	switch name {
	case "gofmt", "goimports", "prettier":
		if _, ok := hasOpt(opts, "w", "write"); !ok {
			return
		}
	}
	for _, f := range ops {
		a.target(f)
	}
}
