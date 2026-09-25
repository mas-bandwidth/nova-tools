sed -i '' -e 's/"+fcall", "\~*"/"+fcall", "~*", "+@hash", "+@connection"/g' internal/nsprint/card/wrapper_test.go
