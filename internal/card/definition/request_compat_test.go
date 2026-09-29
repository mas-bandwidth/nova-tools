package definition

import "github.com/mas-bandwidth/nova-tools/internal/card/request"

// parseAsAdmit reads a document as an admit request of the request package.
func parseAsAdmit(doc []byte) error {
	_, err := request.Parse(doc)
	return err
}
