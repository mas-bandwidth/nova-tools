package fleet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialSeatsAreExplicitAndBounded(t *testing.T) {
	t.Parallel()
	good := `[{"account_id":"rowan","source_seat":"rowan-source"},{"account_id":"rowan","profile_id":"rowan-next","target_seat":"rowan-next-mini"},{"account_id":"personal","profile_id":"rowan-personal","target_seat":"rowan-personal-mini"}]`
	seats, err := ParseCredentialSeats(good)
	require.NoError(t, err)
	require.Len(t, seats, 3)
	assert.Equal(t, "rowan-source", seats[0].SeatName())
	assert.Equal(t, CredentialSeat{AccountID: "personal", ProfileID: "rowan-personal", TargetSeat: "rowan-personal-mini"}, seats[2])
	for _, raw := range []string{
		`null`, `{}`, `[{}]`, `[{"account_id":"x","profile_id":"p","target_seat":"s","token":"secret"}]`,
		`[{"account_id":"x","profile_id":"p","target_seat":"s"},{"account_id":"y","profile_id":"p","target_seat":"t"}]`,
		`[{"account_id":"x","profile_id":"p","target_seat":"s"},{"account_id":"y","profile_id":"q","target_seat":"s"}]`,
		`[{"account_id":"x","profile_id":"p","target_seat":"s"}] true`,
		`[{"account_id":"x","account_id":"y","profile_id":"p","target_seat":"s"}]`,
		`[{"account_id":"x","Account_ID":"y","profile_id":"p","target_seat":"s"}]`,
		`[{"account_id":"x","source_seat":"s","profile_id":"p","target_seat":"t"}]`,
		`[{"account_id":"x","source_seat":""}]`,
		`[{"account_id":"x","source_seat":"s"},{"account_id":"x","profile_id":"p","target_seat":"s"}]`,
	} {
		_, err := ParseCredentialSeats(raw)
		assert.Error(t, err, "raw=%q", raw)
	}
	const marker = "SHOULD_NOT_APPEAR_IN_LOGS"
	_, err = ParseCredentialSeats(`[{"account_id":"x","profile_id":"p","target_seat":"s","` + marker + `":"x"}]`)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), marker)
}
