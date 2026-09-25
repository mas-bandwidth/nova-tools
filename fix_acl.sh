sed -i '' -e 's/"+fcall", "\~*"/"+fcall", "~*", "+@hash", "+@connection"/' internal/nsprint/card/wrapper_test.go
go test ./internal/nsprint/card -run TestClaimIsALibraryFunction
