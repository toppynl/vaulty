set -e
git init -q -b main
git -c user.email=t@t -c user.name=t add -A
git -c user.email=t@t -c user.name=t commit -q -m init
cat >> wiki/b.md <<'BEOF'
- **2026-08-02** | Robin — out of order.
BEOF
cat > wiki/c.md <<'CEOF'
---
title: C
---

# C

body

---

## Timeline

- **2026-08-01** | plain text no separator.
CEOF
