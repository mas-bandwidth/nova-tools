git checkout internal/nsprint/card/end.go
awk '
/^func fcall/ {
    print
    getline
    print "\terr := fn.Load(ctx, st.Client())"
    print "\tif err != nil && !strings.Contains(err.Error(), \"NOPERM\") {"
    next
}
{ print }
' internal/nsprint/card/end.go > end_tmp.go
mv end_tmp.go internal/nsprint/card/end.go
gofmt -w internal/nsprint/card/end.go
go test ./internal/nsprint/card -run TestClaimIsALibraryFunction
