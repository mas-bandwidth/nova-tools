sed -i '' -e 's/adminSt, adminClient := newSprint(t)/_, adminClient := newSprint(t)/' internal/nsprint/card/wrapper_test.go
# Add import
awk '
/^import \(/ {
    print
    print "\t\"github.com/mas-bandwidth/nova-tools/internal/nsprint/store\""
    next
}
{ print }
' internal/nsprint/card/wrapper_test.go > wrapper_test_tmp.go
mv wrapper_test_tmp.go internal/nsprint/card/wrapper_test.go
