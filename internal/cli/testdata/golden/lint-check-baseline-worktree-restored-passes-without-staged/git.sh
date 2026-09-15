set -e
git init -q -b main
git -c user.email=t@t -c user.name=t add -A
git -c user.email=t@t -c user.name=t commit -q -m init
cat > .vaulty-baseline.json <<'JSON'
{"pages": {"wiki/a.md": {"tl006": 2, "tl008": 0, "pg002_tokens": 0}}}
JSON
git -c user.email=t@t -c user.name=t add -A
cat > .vaulty-baseline.json <<'JSON'
{"pages": {"wiki/a.md": {"tl006": 1, "tl008": 0, "pg002_tokens": 0}}}
JSON
