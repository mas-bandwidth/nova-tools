package dshdesktop

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// DSH session format v4 refuses a message whose source kind is empty or the
// retired "plugin" wrapper ("format v4 message requires a producer-owned source
// kind", dsh-session-format-v3-to-v4 lib/index.js source()). A third-party
// plugin owns "plugin:" + its complete name. Pin the shipped receiver bytes.
func TestReceiverSourceKindIsProducerOwned(t *testing.T) {
	src := string(receiverSource)
	decl := regexp.MustCompile(`(?m)^export const sourceKind = '([^']*)';$`).FindStringSubmatch(src)
	require.NotNil(t, decl, "receiver declares one source kind")
	require.Equal(t, "plugin:nova-hook", decl[1])
	require.NotEqual(t, "plugin", decl[1])

	require.Contains(t, src, "source:{kind:sourceKind}", "every native message uses the declared kind")
	require.NotRegexp(t, `kind:\s*['"]plugin['"]`, src, "the V3 plugin wrapper is refused by DSH v4")
	require.NotRegexp(t, `plugin:\s*['"]nova-hook['"]`, src, "no V3 plugin-name field rides on the source")
	require.Len(t, regexp.MustCompile(`source:\s*\{`).FindAllStringIndex(src, -1), 1, "one message construction site")
}
