package hookio

// shellinterp.go — K-170 (b): write calls inside interpreter one-liners and
// here-documents ("python3 -c", "node -e", "perl -e", "ruby -e", "php -r", and
// "python3 - <<'EOF'"). Pattern based: it finds calls whose NAME writes a file
// and reads the path from the call's literal argument. A recognised write whose
// path is not a plain string literal is reported as a dynamic target. A
// program that is only named ("python3 build.py") is not read.

import (
	"regexp"
	"strings"
)

func interpreterKind(name string) string {
	switch {
	case strings.HasPrefix(name, "python"):
		return "python"
	case name == "node" || name == "nodejs" || name == "deno" || name == "bun":
		return "node"
	case name == "ruby":
		return "ruby"
	case name == "perl":
		return "perl"
	case name == "php":
		return "php"
	}
	return ""
}

// interpreter reads the code handed to an interpreter: -c/-e/-E/-r operands, a
// "deno eval" operand, here-documents and here-strings.
func (a *shAnalyzer) interpreter(name string, args []shArg, heredocs []*shHeredoc, herestr []string, depth int) {
	kind := interpreterKind(name)
	if kind == "" {
		return
	}
	var codes []string
	dynamic := false
	for i := 0; i < len(args); i++ {
		x := args[i]
		if x.dyn {
			break
		}
		if !strings.HasPrefix(x.val, "-") || x.val == "-" {
			if name == "deno" && x.val == "eval" && i+1 < len(args) {
				i++
				if args[i].dyn {
					dynamic = true
				} else {
					codes = append(codes, args[i].val)
				}
				break
			}
			break // first operand: a script file, the rest are its arguments
		}
		takes := false
		switch kind {
		case "python":
			takes = x.val == "-c"
		case "node":
			takes = x.val == "-e" || x.val == "--eval" || x.val == "-p" || x.val == "--print"
		case "php":
			takes = x.val == "-r"
		case "ruby", "perl":
			if !strings.HasPrefix(x.val, "--") {
				last := x.val[len(x.val)-1]
				takes = last == 'e' || last == 'E'
			}
		}
		if takes && i+1 < len(args) {
			i++
			if args[i].dyn {
				dynamic = true
			} else {
				codes = append(codes, args[i].val)
			}
		}
	}
	for _, hd := range heredocs {
		codes = append(codes, hd.body)
	}
	codes = append(codes, herestr...)
	if dynamic {
		a.add(ShellWrite{Path: "<interpreter code built at run time>", Dynamic: true})
	}
	for _, c := range codes {
		a.code(kind, c, depth)
	}
}

var (
	rePyOpen     = regexp.MustCompile(`\bopen\s*\(`)
	rePyWriteTxt = regexp.MustCompile(`\.write_(?:text|bytes)\s*\(`)
	rePyCopy     = regexp.MustCompile(`\b(?:shutil\.(?:copy|copy2|copyfile|move|copytree)|os\.(?:rename|replace|link|symlink))\s*\(`)
	rePyShell    = regexp.MustCompile(`\b(?:os\.(?:system|popen)|subprocess\.\w+)\s*\(`)

	reJSWrite = regexp.MustCompile(`\b(?:writeFileSync|writeFile|appendFileSync|appendFile|createWriteStream|writeTextFileSync|writeTextFile|Bun\.write|truncateSync)\s*\(`)
	reJSCopy  = regexp.MustCompile(`\b(?:copyFileSync|copyFile|cpSync|renameSync|rename|linkSync|symlinkSync)\s*\(`)
	reJSExec  = regexp.MustCompile(`\b(?:execSync|exec|spawnSync|spawn|execFileSync|execFile)\s*\(`)

	reRbWrite = regexp.MustCompile(`\b(?:File|IO)\.(?:write|binwrite)\s*\(`)
	reRbOpen  = regexp.MustCompile(`\bFile\.(?:open|new)\s*\(`)
	reRbUtil  = regexp.MustCompile(`\bFileUtils\.(?:cp|cp_r|copy|mv|move|install|ln|ln_s|touch)\s*\(`)
	reSystem  = regexp.MustCompile(`\b(?:system|exec|shell_exec|passthru|popen|proc_open)\s*\(`)
	rePerlOpn = regexp.MustCompile(`\bopen\s*\(?`)

	rePHPPut  = regexp.MustCompile(`\b(?:file_put_contents|fputcsv)\s*\(`)
	rePHPOpen = regexp.MustCompile(`\bfopen\s*\(`)
	rePHPCopy = regexp.MustCompile(`\b(?:copy|rename|symlink|link)\s*\(`)
)

func modeWrites(m string) bool { return strings.ContainsAny(m, "wax+") }

func (a *shAnalyzer) code(kind, code string, depth int) {
	switch kind {
	case "python":
		a.pyCode(code, depth)
	case "node":
		a.callsPath(code, reJSWrite, 0)
		a.callsPath(code, reJSCopy, 1)
		a.callsShell(code, reJSExec, depth, true)
	case "ruby":
		a.rubyCode(code, depth)
	case "perl":
		a.perlCode(code, depth)
	case "php":
		a.callsPath(code, rePHPPut, 0)
		a.phpOpen(code)
		a.callsPath(code, rePHPCopy, 1)
		a.callsShell(code, reSystem, depth, false)
	}
}

// eachCall calls fn with the top-level arguments of every call re matches
// ("name(" at the end of the match), and the text just before the match.
func eachCall(code string, re *regexp.Regexp, fn func(args []string, before string)) {
	for _, m := range re.FindAllStringIndex(code, 64) {
		open := m[1]
		if open == 0 || code[open-1] != '(' {
			if k := strings.IndexByte(code[m[0]:m[1]], '('); k >= 0 {
				open = m[0] + k + 1
			} else {
				continue
			}
		}
		fn(splitArgs(code[open:]), code[:m[0]])
	}
}

// splitArgs splits the text after an opening parenthesis into top-level,
// trimmed arguments, up to the matching close.
func splitArgs(s string) []string {
	var args []string
	depth, start := 1, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				if t := strings.TrimSpace(s[start:i]); t != "" || len(args) > 0 {
					args = append(args, t)
				}
				return args
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
		if i > 4000 {
			break
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		args = append(args, t)
	}
	return args
}

var litRE = regexp.MustCompile("^(?:[rRbBuUfF]{0,2})(\"((?:\\\\.|[^\"\\\\])*)\"|'((?:\\\\.|[^'\\\\])*)'|`([^`]*)`)$")

// strLit returns the value of a plain string-literal argument. An f-string,
// a template string with ${}, or a Ruby/PHP interpolation is not plain.
func strLit(arg string) (string, bool) {
	m := litRE.FindStringSubmatch(strings.TrimSpace(arg))
	if m == nil {
		return "", false
	}
	v := m[2] + m[3] + m[4]
	lower := strings.ToLower(arg)
	if strings.HasPrefix(lower, "f") && strings.Contains(v, "{") || strings.Contains(v, "${") || strings.Contains(v, "#{") {
		return "", false // an interpolating string is not a plain literal
	}
	if strings.HasPrefix(arg, "\"") && (strings.Contains(v, "$") || strings.Contains(v, "@")) {
		return "", false // PHP and Perl interpolate $var and @var in double quotes
	}
	v = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, "\t").Replace(v)
	return v, true
}

func (a *shAnalyzer) pathArg(arg string) {
	if v, ok := strLit(arg); ok {
		a.addPath(v)
		return
	}
	a.add(ShellWrite{Path: "<write call with a path built at run time: " + arg + ">", Dynamic: true})
}

// callsPath records argument index idx of every call re matches.
func (a *shAnalyzer) callsPath(code string, re *regexp.Regexp, idx int) {
	eachCall(code, re, func(args []string, _ string) {
		if idx < len(args) {
			a.pathArg(args[idx])
		}
	})
}

// callsShell analyses a literal command handed to exec/system/popen/subprocess.
// A list-form call (["tee", ".env"]) is analysed as an argv.
func (a *shAnalyzer) callsShell(code string, re *regexp.Regexp, depth int, jsArgv bool) {
	eachCall(code, re, func(args []string, _ string) {
		if len(args) == 0 {
			return
		}
		first := args[0]
		if strings.HasPrefix(first, "[") || (jsArgv && len(args) > 1 && strings.HasPrefix(args[1], "[")) {
			var argv []string
			src := first
			if !strings.HasPrefix(first, "[") {
				src = first + "," + args[1]
			}
			for _, m := range regexp.MustCompile(`"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'`).FindAllStringSubmatch(src, 32) {
				argv = append(argv, m[1]+m[2])
			}
			a.argv(argv, depth)
			return
		}
		if v, ok := strLit(first); ok {
			st := a.save()
			a.script(v, depth+1)
			a.restore(st)
		}
	})
}

func (a *shAnalyzer) argv(argv []string, depth int) {
	if len(argv) == 0 {
		return
	}
	words := make([]shArg, len(argv))
	for i, s := range argv {
		words[i] = shArg{resolved: resolved{val: s, raw: s, quoted: true}, w: &shWord{parts: []shPart{{kind: partLit, text: s, quoted: true}}}}
	}
	st := a.save()
	a.dispatch(words, nil, nil, depth+1)
	a.restore(st)
}

func (a *shAnalyzer) pyCode(code string, depth int) {
	eachCall(code, rePyOpen, func(args []string, before string) {
		if len(args) == 0 {
			return
		}
		mode, haveMode := "", false
		kwMode := ""
		for _, x := range args[1:] {
			if k, v, ok := strings.Cut(x, "="); ok && strings.TrimSpace(k) == "mode" {
				kwMode = strings.TrimSpace(v)
			}
		}
		if kwMode != "" {
			args = append(append([]string{}, args[:1]...), kwMode)
		}
		methodForm := strings.HasSuffix(strings.TrimSpace(before), ".") && !strings.HasSuffix(strings.TrimSpace(before), "io.") && !strings.HasSuffix(strings.TrimSpace(before), "codecs.")
		if methodForm {
			// Path(x).open("w"): the receiver is the path, args[0] the mode.
			if m, ok := strLit(args[0]); ok {
				if modeWrites(m) {
					if p := receiverLit(before); p != "" {
						a.addPath(p)
					} else {
						a.add(ShellWrite{Path: "<open() for writing on a path built at run time>", Dynamic: true})
					}
				}
			}
			return
		}
		if len(args) > 1 {
			if m, ok := strLit(args[1]); ok {
				mode, haveMode = m, true
			} else {
				mode, haveMode = "w", true // a mode we cannot read: assume the worst
			}
		}
		if !haveMode || !modeWrites(mode) {
			return
		}
		a.pathArg(args[0])
	})
	eachCall(code, rePyWriteTxt, func(_ []string, before string) {
		if p := receiverLit(before + "."); p != "" {
			a.addPath(p)
		} else {
			a.add(ShellWrite{Path: "<write_text/write_bytes on a path built at run time>", Dynamic: true})
		}
	})
	a.callsPath(code, rePyCopy, 1)
	a.callsShell(code, rePyShell, depth, false)
}

var receiverRE = regexp.MustCompile(`Path\(\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')\s*\)\s*\.?\s*$`)

// receiverLit returns the literal path of a trailing Path("...") receiver.
func receiverLit(before string) string {
	m := receiverRE.FindStringSubmatch(strings.TrimRight(before, ". \t"))
	if m == nil {
		return ""
	}
	v, _ := strLit(m[1])
	return v
}

func (a *shAnalyzer) rubyCode(code string, depth int) {
	a.callsPath(code, reRbWrite, 0)
	eachCall(code, reRbOpen, func(args []string, _ string) {
		if len(args) < 2 {
			return
		}
		if mode, ok := strLit(args[1]); !ok || modeWrites(mode) {
			a.pathArg(args[0])
		}
	})
	a.callsPath(code, reRbUtil, 1)
	a.callsShell(code, reSystem, depth, false)
}

func (a *shAnalyzer) perlCode(code string, depth int) {
	for _, m := range rePerlOpn.FindAllStringIndex(code, 32) {
		args := splitArgs(code[m[1]:])
		switch {
		case len(args) >= 3:
			if mode, ok := strLit(args[1]); ok && strings.ContainsAny(mode, ">+") {
				a.pathArg(args[2])
			}
		case len(args) == 2:
			if v, ok := strLit(args[1]); ok {
				t := strings.TrimSpace(v)
				if strings.HasPrefix(t, ">") || strings.HasPrefix(t, "+<") || strings.HasPrefix(t, "+>") {
					a.addPath(strings.TrimSpace(strings.TrimLeft(t, ">+<")))
				}
			}
		}
	}
	a.callsShell(code, reSystem, depth, false)
}

func (a *shAnalyzer) phpOpen(code string) {
	eachCall(code, rePHPOpen, func(args []string, _ string) {
		if len(args) > 1 {
			if mode, ok := strLit(args[1]); !ok || strings.ContainsAny(mode, "waxc+") {
				a.pathArg(args[0])
			}
		}
	})
}
