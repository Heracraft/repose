import re
def bash_cat(c):
    s = c.strip()
    if re.search(r'\bsleep\b|until |while .*sleep|--watch|gh run watch|\bwait\b|inotifywait|tail -f', s): return "wait/poll"
    if re.search(r'\b(go test|pytest|vitest|playwright|pnpm (test|check|run test)|npm (test|run test)|cargo test|just )', s): return "test"
    if re.search(r'\b(go build|go vet|golangci|nix (build|flake|develop|eval)|pnpm (build|run build)|npm run build|cargo build|tsc|make\b|svelte-check)', s): return "build"
    if re.search(r'\bssh\b|docker exec|repose exec|\bscp\b', s): return "remote exec"
    if re.search(r'^\s*(cd [^&;]+(&&|;)\s*)?git\b', s): return "git"
    if re.search(r'\b(rg|grep|find|fd)\b', s): return "search"
    if re.search(r'\b(cat|head|tail|sed -n|wc|ls|tree|jq|less|bat)\b', s): return "read"
    if re.search(r'\b(gh|curl|aws|az)\b', s): return "api call"
    if re.search(r'\b(ps|pgrep|systemctl|journalctl|df|free|top|ss|lsof|dmesg)\b', s): return "inspect system"
    if re.search(r'\b(python3?|node|bash|sh)\b', s): return "script"
    return "other"

