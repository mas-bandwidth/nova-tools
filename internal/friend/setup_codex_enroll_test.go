package friend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexEnrollmentRequiresNativeConsentThenSubscriptionStatus(t *testing.T) {
	t.Parallel()
	p, err := PlanCodexEnrollment(codexSetupOptions(), NewMemFS(), "account label", 10*time.Minute)
	require.NoError(t, err)
	consent, status := p.Steps[len(p.Steps)-2], p.Steps[len(p.Steps)-1]
	require.NotNil(t, consent.Command)
	assert.Equal(t, []string{"-c", `log_dir="/workers/config/log"`, "login", "--device-auth"}, consent.Command.Args)
	assert.True(t, consent.Command.ConsentOnTerminal)
	assert.False(t, status.Command.ConsentOnTerminal)
	assert.Equal(t, 10*time.Minute, consent.Command.Timeout)
	require.NoError(t, validateSetupCommand(*consent.Command))
	_, err = consent.Validate(SetupOutput{})
	require.NoError(t, err)
	_, err = consent.Validate(SetupOutput{Stdout: "private-device-code"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-device-code")
	_, err = status.Validate(SetupOutput{Stdout: "Not logged in", Exit: 1})
	require.Error(t, err, "native exit zero alone must not certify a profile")
	facts, err := status.Validate(SetupOutput{Stdout: "Logged in using ChatGPT"})
	require.NoError(t, err)
	assert.Equal(t, "local_chatgpt_auth", facts[0].State)
}

func TestCodexEnrollmentRefusesUnboundedOrUnidentifiedSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, label, profile string
		timeout              time.Duration
	}{
		{"missing label", "", "account", time.Minute},
		{"multiline label", "account\nlabel", "account", time.Minute},
		{"display profile", "account", "account (space)", time.Minute},
		{"missing timeout", "account", "account", 0},
		{"too long", "account", "account", 16 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := codexSetupOptions()
			o.Profile = tc.profile
			_, err := PlanCodexEnrollment(o, NewMemFS(), tc.label, tc.timeout)
			require.Error(t, err)
		})
	}
	_, err := CodexConsentContext(context.Background(), nil)
	require.Error(t, err)
}

func TestCodexEnrollmentRefusesUnattendedBeforeSetupWrites(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	p, err := PlanSetup(codexSetupOptions(), fs, func(o SetupOptions, fs SettingsFS) (SetupPlan, error) {
		return PlanCodexEnrollment(o, fs, "account", time.Minute)
	})
	require.NoError(t, err)
	runner := NativeSetupRunner()
	runner.FS = fs
	_, err = runner.Apply(context.Background(), p, "/workers/config/.nova-friend-setup")
	require.ErrorContains(t, err, "terminal")
	e, err := fs.Lstat("/workers/config")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, e.Kind)
}
