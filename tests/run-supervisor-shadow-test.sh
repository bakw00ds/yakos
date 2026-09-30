#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-shadow-test.sh — K-111 P2b regression test for the shadow
# decision call in lib/hooks/supervisor-stream.sh and its Go twin.
#
# Contract under test (nothing here may block, alter or delay a tool call):
#   (n) provider none (default, or unknown, or jev without a key, or the kill
#       switch): the hook never starts a decision call.
#   (m) provider mock: both sides start `yakos decide supervisor-prefilter
#       --shadow --local <pass|escalate> ...` with the SAME argv and the SAME
#       state JSON on stdin (a fake CLI records both).
#   (e) end to end with the real Go binary and the mock provider: both sides
#       leave the same decision-log record (mode shadow, local verdict); a
#       provider error is a silent fail-open (hook exit 0, error class logged).
#   (c) `yakos decide compare` reads those records back.
#
# The Go half needs bin/yakos (make build). Run under `bash` and `/bin/bash`
# (3.2). No network and no key: the mock provider only.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/supervisor-stream.sh"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
HAVE_GO=1; [ -x "$GO_BINARY" ] || HAVE_GO=0

unset YAKOS_ROOT YAKOS_LIB YAKOS_CLI YAKOS_SUPERVISOR_DISABLE YAKOS_DECISION_PROVIDER \
      YAKOS_DECISION_DISABLE YAKOS_DECISION_MOCK TYPESAFE_API_KEY
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-shadow-test-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

sides="bash"; [ "$HAVE_GO" = 1 ] && sides="bash go"

mksb() { # mksb <name> <yml-body>
    local sb="$TMP/$1"
    mkdir -p "$sb/.claude" "$sb/work/current/logs" "$sb/home"
    printf '%s' "$2" > "$sb/.yakos.yml"
    printf '%s' "$sb"
}
# fake CLI: records argv (one ARG: line each) and the state, which arrives on
# stdin (bash side) or in the --state-file the Go side hands over; then exits 0.
mkfake() { # mkfake <record-prefix>
    local f="$TMP/fake-yakos-$RANDOM"
    cat > "$f" <<FAKE
#!/bin/sh
for a in "\$@"; do printf 'ARG:%s\\n' "\$a" >> "$1.argv"; done
sf=""; prev=""
for a in "\$@"; do [ "\$prev" = "--state-file" ] && sf="\$a"; prev="\$a"; done
if [ -n "\$sf" ]; then cat "\$sf" > "$1.stdin"; else cat > "$1.stdin"; fi
FAKE
    chmod +x "$f"; printf '%s' "$f"
}
# argv without the Go-only state hand-over flags, so the two sides compare equal.
strip_handover() { awk 'skip > 0 { skip--; next } /^ARG:--state-file$/ { skip = 1; next } /^ARG:--consume-state-file$/ { next } { print }'; }
run_payload() { # run_payload <side> <sandbox> <json> [env assignments...]
    local side="$1" sb="$2" json="$3"; shift 3
    if [ "$side" = "bash" ]; then
        printf '%s' "$json" | env "$@" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "${BASH:-bash}" "$HOOK" >"$sb/stdout" 2>"$sb/stderr"
    else
        printf '%s' "$json" | env "$@" HOME="$sb/home" YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "$GO_BINARY" hook run supervisor-stream >"$sb/stdout" 2>"$sb/stderr"
    fi
    echo $? > "$sb/rc"
}
bash_payload() { jq -nc --arg c "$1" '{session_id:"sess-1",hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:$c}}'; }
edit_payload() { jq -nc --arg f "$1" --arg b "$2" '{session_id:"sess-1",hook_event_name:"PostToolUse",tool_name:"Edit",tool_input:{file_path:$f,new_string:$b}}'; }
wait_for() { # wait_for <file> [min-lines]
    local i=0 n="${2:-1}"
    while [ "$i" -lt 100 ]; do
        [ -f "$1" ] && [ "$(wc -l < "$1" | tr -d ' ')" -ge "$n" ] && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}
wait_for_file() { # wait_for_file <file>: exists and non-empty
    local i=0
    while [ "$i" -lt 100 ]; do
        [ -s "$1" ] && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}
norm() { sed "s|$1|<SB>|g"; }

YML_PLAIN=$'supervisor:\n  score_every_n_calls: 1000\n'

# ---- (n) provider none never starts a call -----------------------------------
# A project .yakos.yml can never ENABLE a provider: only the env var or the
# user-level ~/.yakos-state/decision-policy.yml can.
n=0
for spec in "default|none|" "none|none|YAKOS_DECISION_PROVIDER=none" \
            "bogus|none|YAKOS_DECISION_PROVIDER=bogus" \
            "jev-nokey|none|YAKOS_DECISION_PROVIDER=jev" \
            "killswitch|none|YAKOS_DECISION_DISABLE=1 YAKOS_DECISION_PROVIDER=mock" \
            "project-jev-only|jev|TYPESAFE_API_KEY=set" \
            "project-mock-only|mock|" \
            "policy-vetoed|none|POLICY=mock" \
            "policy-world-writable|none|POLICYWW=mock" \
            "policy-group-writable|none|POLICYGW=mock" \
            "policy-symlink|none|POLICYLN=mock"; do
    label="${spec%%|*}"; rest="${spec#*|}"; proj="${rest%%|*}"; envs="${rest#*|}"
    for side in $sides; do
        n=$((n + 1))
        yml=$'supervisor:\n  score_every_n_calls: 1000\n'
        [ "$label" = default ] || yml="${yml}decisions:"$'\n'"  provider: $proj"$'\n'
        sb="$(mksb "n-$side-$label" "$yml")"
        fake="$(mkfake "$sb/rec")"
        case "$envs" in
            POLICYWW=*|POLICYGW=*|POLICYLN=*)
                # An untrusted policy file must not enable the provider (and the
                # project file is irrelevant: provider none there is not a veto
                # under test, the file's trust is).
                mkdir -p "$sb/home/.yakos-state"
                pf="$sb/home/.yakos-state/decision-policy.yml"
                printf 'provider: mock\n' > "$pf"
                case "$envs" in
                    POLICYWW=*) chmod 666 "$pf" ;;
                    POLICYGW=*) chmod 660 "$pf" ;;
                    POLICYLN=*) mv "$pf" "$pf.real"; ln -s "$pf.real" "$pf" ;;
                esac
                envs=""
                rm -f "$sb/.yakos.yml"
                ;;
            POLICY=*) mkdir -p "$sb/home/.yakos-state"; printf 'provider: %s\n' "${envs#POLICY=}" > "$sb/home/.yakos-state/decision-policy.yml"; envs="" ;;
        esac
        # shellcheck disable=SC2086
        run_payload "$side" "$sb" "$(bash_payload 'rm -rf /')" "YAKOS_CLI=$fake" $envs
        sleep 0.3
        if [ ! -e "$sb/rec.argv" ] && [ "$(cat "$sb/rc")" = 0 ]; then
            ok "(n) $side provider-none case '$label' starts no decision call"
        else
            bad "(n) $side provider-none case '$label' started a call or failed (rc=$(cat "$sb/rc"))"
        fi
    done
done
# The user-level policy file DOES enable it; the env var wins over a project veto.
for side in $sides; do
    sb="$(mksb "np-$side" $'supervisor:\n  score_every_n_calls: 1000\n')"
    mkdir -p "$sb/home/.yakos-state"; printf 'provider: mock  # user switch\n' > "$sb/home/.yakos-state/decision-policy.yml"
    fake="$(mkfake "$sb/rec")"
    run_payload "$side" "$sb" "$(bash_payload 'ls')" "YAKOS_CLI=$fake"
    if wait_for "$sb/rec.argv" 5; then ok "(n) $side user policy file enables the provider"; else bad "(n) $side user policy file did not enable the provider"; fi
    sb="$(mksb "nv-$side" $'supervisor:\n  score_every_n_calls: 1000\ndecisions:\n  provider: none\n')"
    fake="$(mkfake "$sb/rec")"
    run_payload "$side" "$sb" "$(bash_payload 'ls')" "YAKOS_CLI=$fake" "YAKOS_DECISION_PROVIDER=mock"
    if wait_for "$sb/rec.argv" 5; then ok "(n) $side env var wins over the project veto"; else bad "(n) $side env var did not win over the project veto"; fi
done

# ---- (m) provider mock: same argv + same state on both sides -------------------
m=0
for spec in "rm|bash|rm -rf /" "ls|bash|ls -la" "edit-in-plan|edit|api.go" "edit-out-of-plan|edit|other.go"; do
    label="${spec%%|*}"; rest="${spec#*|}"; kind="${rest%%|*}"; arg="${rest#*|}"
    outs=""
    for side in $sides; do
        m=$((m + 1))
        sb="$(mksb "m-$side-$label" "$YML_PLAIN")"
        printf 'fix the retry test in api.go\n' > "$sb/work/current/decisions.md"
        printf 'touch api.go only\n' > "$sb/work/current/plan.md"
        fake="$(mkfake "$sb/rec")"
        if [ "$kind" = bash ]; then json="$(bash_payload "$arg")"; else json="$(edit_payload "$sb/$arg" 'x := 1')"; fi
        run_payload "$side" "$sb" "$json" "YAKOS_CLI=$fake" "YAKOS_DECISION_PROVIDER=mock"
        [ "$(cat "$sb/rc")" = 0 ] && ok "(m) $side $label hook exit 0" || bad "(m) $side $label hook rc=$(cat "$sb/rc")"
        [ ! -s "$sb/stdout" ] && ok "(m) $side $label hook stdout untouched" || bad "(m) $side $label hook wrote stdout"
        if wait_for "$sb/rec.argv" 5 && wait_for_file "$sb/rec.stdin"; then
            ok "(m) $side $label started the decision call"
        else
            bad "(m) $side $label never started the decision call"
        fi
        sleep 0.1
        outs="$outs$( { strip_handover < "$sb/rec.argv" | norm "$sb"; norm "$sb" < "$sb/rec.stdin" | jq -cS .; } 2>&1)"$'\n=====\n'
    done
    if [ "$HAVE_GO" = 1 ]; then
        a="$(printf '%s' "$outs" | awk 'BEGIN{RS="=====\n"} NR==1')"; b="$(printf '%s' "$outs" | awk 'BEGIN{RS="=====\n"} NR==2')"
        if [ "$a" = "$b" ] && [ -n "$a" ]; then ok "(m) $label argv+state identical bash/go"; else bad "(m) $label differs: bash=[$a] go=[$b]"; fi
    fi
    if [ "$label" = rm ]; then
        printf '%s' "$outs" | grep -q -- '--local' && printf '%s' "$outs" | grep -q 'ARG:escalate' && ok "(m) rm escalates locally" || bad "(m) rm not recorded as escalate"
        printf '%s' "$outs" | grep -q 'ARG:risk-regex' && ok "(m) trigger kind passed without the pattern" || bad "(m) trigger kind missing"
    fi
    if [ "$label" = ls ]; then
        printf '%s' "$outs" | grep -q 'ARG:pass' && ok "(m) ls passes locally" || bad "(m) ls not recorded as pass"
    fi
    if [ "$label" = edit-in-plan ]; then
        printf '%s' "$outs" | grep -q '"plan_mentions_path":true' && ok "(m) plan_mentions_path true" || bad "(m) plan_mentions_path not true"
    fi
    if [ "$label" = edit-out-of-plan ]; then
        printf '%s' "$outs" | grep -q '"plan_mentions_path":false' && ok "(m) plan_mentions_path false" || bad "(m) plan_mentions_path not false"
    fi
done

# ---- (e) end to end: real binary, mock provider ---------------------------------
if [ "$HAVE_GO" = 1 ]; then
    FX_OK="$TMP/mock-ok.json"
    FX_ERR="$TMP/mock-err.json"
    cp "$REPO_ROOT/lib/decisions/examples/supervisor-prefilter.mock.json" "$FX_OK"
    printf '{"error":"timeout"}' > "$FX_ERR"
    for fx in ok err; do
        recs=""
        for side in $sides; do
            sb="$(mksb "e-$side-$fx" "$YML_PLAIN")"
            mock="$FX_OK"; [ "$fx" = err ] && mock="$FX_ERR"
            run_payload "$side" "$sb" "$(bash_payload 'git push --force origin main')" \
                "YAKOS_CLI=$GO_BINARY" "YAKOS_ROOT=$REPO_ROOT" "YAKOS_DECISION_MOCK=$mock" "YAKOS_DECISION_PROVIDER=mock"
            [ "$(cat "$sb/rc")" = 0 ] && ok "(e) $side $fx hook exit 0" || bad "(e) $side $fx hook rc=$(cat "$sb/rc")"
            log="$sb/home/.yakos-state/decision-log.ndjson"
            if wait_for "$log" 1; then
                ok "(e) $side $fx decision log written"
            else
                bad "(e) $side $fx no decision log record"
                continue
            fi
            recs="$recs$(tail -n 1 "$log" | jq -cS 'del(.ts, .id, .latency_ms)')"$'\n'
            rec="$(tail -n 1 "$log")"
            [ "$(printf '%s' "$rec" | jq -r '.mode')" = shadow ] && ok "(e) $side $fx mode shadow" || bad "(e) $side $fx mode not shadow"
            [ "$(printf '%s' "$rec" | jq -r '.local_verdict')" = escalate ] && ok "(e) $side $fx local verdict recorded" || bad "(e) $side $fx local verdict missing"
            if [ "$fx" = err ]; then
                [ "$(printf '%s' "$rec" | jq -r '.status')" = timeout ] && ok "(e) $side error class logged (fail-open)" || bad "(e) $side error class not logged"
            else
                [ "$(printf '%s' "$rec" | jq -r '.answers.risk_class.choice')" = dangerous ] && ok "(e) $side shadow verdict logged" || bad "(e) $side shadow verdict missing"
            fi
            # The hook's own log and buffer are exactly what a no-provider run leaves.
            grep -q 'pre-filter: ESCALATE' "$sb/work/current/logs/supervisor-stream.ndjson" && ok "(e) $side $fx escalation path unchanged" || bad "(e) $side $fx escalation log missing"
        done
        if [ "$(printf '%s' "$recs" | grep -c .)" = 2 ]; then
            a="$(printf '%s' "$recs" | sed -n 1p)"; b="$(printf '%s' "$recs" | sed -n 2p)"
            [ "$a" = "$b" ] && ok "(e) $fx decision-log record identical bash/go" || bad "(e) $fx record differs: bash=$a go=$b"
        fi
    done

    # ---- (c) compare reads the records back --------------------------------------
    sb="$TMP/e-go-ok"
    out="$(env HOME="$sb/home" YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" decide compare supervisor-prefilter --json --include-mock 2>&1)"
    if printf '%s' "$out" | jq -e '.records == 1 and .answered == 1 and .both_escalate == 1 and .agreement == 1' >/dev/null 2>&1; then
        ok "(c) decide compare: 1 shadow record, escalate on both sides, agreement 1"
    else
        bad "(c) decide compare output unexpected: $out"
    fi
    out="$(env HOME="$sb/home" YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" decide compare supervisor-prefilter --json 2>&1)"
    if printf '%s' "$out" | jq -e '.records == 0 and .mock_skipped == 1' >/dev/null 2>&1; then
        ok "(c) decide compare leaves mock-provider records out by default"
    else
        bad "(c) mock record was counted: $out"
    fi
    sb="$TMP/e-go-err"
    out="$(env HOME="$sb/home" YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" decide compare supervisor-prefilter --json --include-mock 2>&1)"
    if printf '%s' "$out" | jq -e '.records == 1 and .answered == 0 and .errors.timeout == 1' >/dev/null 2>&1; then
        ok "(c) decide compare counts the fail-open record"
    else
        bad "(c) decide compare error output unexpected: $out"
    fi
else
    echo "  SKIP (e)/(c): bin/yakos not built"
fi

# ---- (s) state hand-over and credentials at the call site ---------------------
if [ "$HAVE_GO" = 1 ]; then
    # The Go side hands the state over in a private file that decide deletes.
    sb="$TMP/e-go-ok"
    left="$(find "$sb/home/.yakos-state" -name 'shadow-state-*' 2>/dev/null | wc -l | tr -d ' ')"
    [ "$left" = 0 ] && ok "(s) go: no state file left behind after decide ran" || bad "(s) go: $left state file(s) left behind"

    # jq argv must not carry the raw preview (world-readable in /proc on Linux).
    SHIM="$TMP/shim"; mkdir -p "$SHIM"
    REALJQ="$(command -v jq)"
    printf '#!/bin/sh\nprintf "%%s\\n" "$(printf "%%s" "$*" | tr "\\n" " ")" >> "%s/jq-argv.txt"\nexec "%s" "$@"\n' "$TMP" "$REALJQ" > "$SHIM/jq"
    chmod +x "$SHIM/jq"
    sb="$(mksb "s-argv" "$YML_PLAIN")"
    printf 'INTENT-MARKER-XYZ fix the retry test\n' > "$sb/work/current/decisions.md"
    fake="$(mkfake "$sb/rec")"
    run_payload bash "$sb" "$(bash_payload 'echo PREVIEW-MARKER-QRS')" "YAKOS_CLI=$fake" "YAKOS_DECISION_PROVIDER=mock" "PATH=$SHIM:$PATH"
    wait_for "$sb/rec.argv" 5 >/dev/null
    # Only the state-building jq call is under test (the buffer event predates this and carries its own redacted preview).
    if grep 'command_or_diff_preview' "$TMP/jq-argv.txt" 2>/dev/null | grep -q 'PREVIEW-MARKER-QRS\|INTENT-MARKER-XYZ'; then bad "(s) bash: preview or intent on jq argv"; else ok "(s) bash: preview and intent stay off jq argv"; fi
    if grep -q 'command_or_diff_preview' "$TMP/jq-argv.txt"; then ok "(s) bash: the state-building jq call was observed"; else bad "(s) bash: state jq call not observed by the shim"; fi
    if grep -q 'PREVIEW-MARKER-QRS' "$sb/rec.stdin" && grep -q 'INTENT-MARKER-XYZ' "$sb/rec.stdin"; then ok "(s) bash: state still carries preview and intent"; else bad "(s) bash: state lost preview or intent"; fi

    # Loopback capture server standing in for the provider: flag-borne credentials never leave.
    cat > "$TMP/capture.py" <<'PY'
import http.server, json, sys
out, portfile = sys.argv[1], sys.argv[2]
ANS = {"risk_class": {"type": "choice", "choice": "benign", "probabilities": {"benign": 0.9, "needs_review": 0.05, "dangerous": 0.05}, "confidence": 0.9},
       "in_stated_scope": {"type": "noul", "noul": 0.5}, "bypasses_hard_control": {"type": "noul", "noul": 0.1}}
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        with open(out, "ab") as f:
            f.write(body + b"\n")
        resp = json.dumps({"model": "jev-1.13.0", "answers": ANS, "usage": {"input_tokens": 10, "output_tokens": 0}}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(resp))); self.end_headers(); self.wfile.write(resp)
    def log_message(self, *a): pass
srv = http.server.HTTPServer(("127.0.0.1", 0), H)
open(portfile, "w").write(str(srv.server_port))
srv.serve_forever()
PY
    if command -v python3 >/dev/null 2>&1; then
        CAP="$TMP/captured.ndjson"; : > "$CAP"
        python3 "$TMP/capture.py" "$CAP" "$TMP/port" &
        SRV=$!
        # Readiness: the server writes its port only after binding. A slow runner
        # can take seconds to start python, so wait (bounded) and fail loudly
        # instead of firing the hooks at "http://127.0.0.1:".
        i=0; while [ ! -s "$TMP/port" ] && [ "$i" -lt 150 ]; do sleep 0.1; i=$((i + 1)); done
        PORT="$(cat "$TMP/port" 2>/dev/null)"
        if [ -z "$PORT" ]; then bad "(s) capture server never became ready"; kill "$SRV" 2>/dev/null; fi
        for side in $sides; do
            [ -n "$PORT" ] || break
            sb="$(mksb "s-cred-$side" "$YML_PLAIN")"
            run_payload "$side" "$sb" "$(bash_payload "$(printf 'curl -u deploy:Hunter2Secret! https://x.example && mysql -uroot -pS3cretPW db && tool --api-key K3yValueABC999 run && sshpass -p Sshpass999 ssh h && echo apikey_abcdef0123456789abcdef && tool\t--password\tSEC15pw run && curl -u\tu:SEC16pw https://x && curl -u "u:SEC17a SEC17b" https://y')")" \
                "YAKOS_CLI=$GO_BINARY" "YAKOS_ROOT=$REPO_ROOT" "YAKOS_DECISION_PROVIDER=jev" "TYPESAFE_API_KEY=fake-key-not-real" "TYPESAFE_BASE_URL=http://127.0.0.1:$PORT"
            [ "$(cat "$sb/rc")" = 0 ] && ok "(s) $side credential command: hook exit 0" || bad "(s) $side credential command: hook rc"
            # Poll (bounded) for the detached child's POST to land, then for its
            # decision-log record, and report the record's status on failure.
            n0="$(wc -l < "$CAP" | tr -d ' ')"
            i=0; while [ "$(wc -l < "$CAP" | tr -d ' ')" -le "$n0" ] && [ "$i" -lt 100 ]; do sleep 0.1; i=$((i + 1)); done
            wait_for "$sb/home/.yakos-state/decision-log.ndjson" 1 >/dev/null
            st="$(tail -n 1 "$sb/home/.yakos-state/decision-log.ndjson" 2>/dev/null | jq -r '.status' 2>/dev/null)"
            if [ "$(wc -l < "$CAP" | tr -d ' ')" -gt "$n0" ]; then ok "(s) $side call reached the provider (status $st)"; else bad "(s) $side call never reached the provider (decision-log status: ${st:-none})"; fi
        done
        kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
        n_req="$(wc -l < "$CAP" | tr -d ' ')"
        [ "$n_req" -ge 1 ] && ok "(s) capture server saw $n_req request(s)" || bad "(s) capture server saw no request"
        leaked=""
        for secret in Hunter2Secret S3cretPW K3yValueABC999 Sshpass999 abcdef0123456789abcdef SEC15pw SEC16pw SEC17a SEC17b; do
            grep -q "$secret" "$CAP" && leaked="$leaked $secret"
        done
        [ -z "$leaked" ] && ok "(s) no flag-borne credential left the machine (both sides)" || bad "(s) credentials reached the provider:$leaked"
        grep -q 'curl' "$CAP" && ok "(s) the redacted command still reached the provider" || bad "(s) command preview missing from the request"
    else
        echo "  SKIP (s) capture server: python3 not found"
    fi
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
