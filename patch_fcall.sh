sed -i '' -e 's/if err := fn.Load/err := fn.Load(ctx, st.Client()); if err != nil \&\& !strings.Contains(err.Error(), "NOPERM") /' internal/nsprint/card/end.go
