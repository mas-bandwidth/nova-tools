package sprintwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientReviewRejectsAResultWithoutAnExplicitStatus(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"missing result fields": `{"results":[{}]}`,
		"null result":           `{"results":[null]}`,
		"null code":             `{"results":[{"code":null,"stdout":"","stderr":""}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := Client{Addr: "sprint.test:6390", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				_, _ = rec.WriteString(body) // ignored: the recorder takes every write
				return rec.Result(), nil
			})}}
			_, err := c.Do(context.Background(), []string{"finish", "--as", "m1"})
			assert.Error(t, err, "a malformed response is unknown, not success")
		})
	}
}
