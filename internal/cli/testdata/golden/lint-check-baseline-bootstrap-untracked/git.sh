set -e
git init -q -b main
git -c user.email=t@t -c user.name=t add wiki/a.md
git -c user.email=t@t -c user.name=t commit -q -m init
