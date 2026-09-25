# Let's add a fn.Load as admin before calling Claim
awk '
/client := redis.NewClient/ {
    print "\tfn.Load(ctx, adminClient)"
    print
    next
}
{ print }
' internal/nsprint/card/wrapper_test.go > wrapper_test_tmp.go
mv wrapper_test_tmp.go internal/nsprint/card/wrapper_test.go
gofmt -w internal/nsprint/card/wrapper_test.go
go test ./internal/nsprint/card -run TestClaimIsALibraryFunction
