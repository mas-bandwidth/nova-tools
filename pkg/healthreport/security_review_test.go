package healthreport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONCaseAliasesCannotReplaceACanonicalField(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	raw := string(encoded(t, failed(e)))
	for _, bad := range []string{
		strings.Replace(raw, `"sequence":7`, `"sequence":7,"Sequence":8`, 1),
		strings.Replace(raw, `"sequence":7`, `"Sequence":7`, 1),
		strings.Replace(raw, `"friend":"bob"`, `"friend":"alice","Friend":"bob"`, 1),
	} {
		_, err := ParseEvent([]byte(bad), e)
		require.Error(t, err, "only canonical schema keys may select an event field")
	}
}

func TestCapabilityRedactsBothValueAndPointerFormatting(t *testing.T) {
	t.Parallel()
	c, err := NewCapability(enrolled("https://127.0.0.1:8443"+Path), secret())
	require.NoError(t, err)
	for _, value := range []any{c, *c} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			assert.Equal(t, "health capability (secret redacted)", fmt.Sprintf(format, value))
		}
	}
}

func TestDefaultLoginMustBeExplicitInTheDescriptorAndEvent(t *testing.T) {
	t.Parallel()
	e := enrolled("https://127.0.0.1:8443" + Path)
	e.User = ""
	got, err := ParseEnrollment(encoded(t, e))
	require.NoError(t, err)
	assert.Equal(t, e, got)
	raw := strings.Replace(string(encoded(t, e)), `,"user":""`, "", 1)
	_, err = ParseEnrollment([]byte(raw))
	require.Error(t, err, "missing login is not an explicit default login")
	event := failed(e)
	_, err = ParseEvent(encoded(t, event), e)
	require.NoError(t, err)
	raw = strings.Replace(string(encoded(t, event)), `,"user":""`, "", 1)
	_, err = ParseEvent([]byte(raw), e)
	require.Error(t, err)
}

func TestCapabilityRedactsNumericAndOtherFormattingVerbs(t *testing.T) {
	t.Parallel()
	c, err := NewCapability(enrolled("https://127.0.0.1:8443"+Path), secret())
	require.NoError(t, err)
	for _, value := range []any{c, *c} {
		for _, format := range []string{"%d", "%x", "%#x", "%b", "%q", "%s", "%e", "%g", "%f", "%.2v"} {
			assert.Equal(t, "health capability (secret redacted)", fmt.Sprintf(format, value))
		}
	}
}

func TestCapabilityTypeAndPointerFormatsNeverExposeSecretBytes(t *testing.T) {
	t.Parallel()
	c, err := NewCapability(enrolled("https://127.0.0.1:8443"+Path), secret())
	require.NoError(t, err)
	numeric := fmt.Sprint(*c.secret)
	hexadecimal := fmt.Sprintf("%x", *c.secret)
	for _, value := range []any{c, *c} {
		for _, format := range []string{"%p", "%T", "%w"} {
			got := fmt.Sprintf(format, value)
			assert.NotContains(t, got, secret())
			assert.NotContains(t, got, numeric)
			assert.NotContains(t, got, hexadecimal)
		}
	}
}
