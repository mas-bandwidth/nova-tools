import re
with open('internal/nsprint/task/claim_test.go', 'r') as f:
    code = f.read()

code = code.replace('"crypto/sha1"\\n\\t"encoding/hex"', '')
code = code.replace('"crypto/sha256"', '"crypto/sha1"')
code = code.replace('sum := sha256.Sum256([]byte(winner.Token))', 'sum := sha1.Sum([]byte(winner.Token))')

with open('internal/nsprint/task/claim_test.go', 'w') as f:
    f.write(code)
