import re
with open('internal/nsprint/task/take.go', 'r') as f:
    code = f.read()

code = code.replace('"crypto/sha256"', '"crypto/sha1"')
code = code.replace('sum := sha256.Sum256([]byte(token))', 'sum := sha1.Sum([]byte(token))')

with open('internal/nsprint/task/take.go', 'w') as f:
    f.write(code)
