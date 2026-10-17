package ntable

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// RowValueForm names the transport form for the caller's per-form counter
// (S2). Legacy is supplied by the caller that knows the record's schema;
// opaque JSON alone cannot distinguish schema JSON from legacy JSON.
func RowValueForm(value string, legacy bool) (schemaread.Form, error) {
	if err := rowValueText(value); err != nil {
		return schemaread.FormNone, err
	}
	reader := schemaread.NewReader(&schemaread.Codec[string]{Legacy: func([]byte) bool { return legacy }})
	return reader.Detect([]byte(value)), nil
}

// rowValueText holds S2's opaque text rule: binary images arrive as padded
// standard base64; this store never interprets the image or a JSON text value.
func rowValueText(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("row value REFUSED: invalid_utf8; run: nova-table help")
	}
	return nil
}

// checkRowValues checks envelope strings before JSON can silently replace
// invalid UTF-8 (S2). Reflection visits the envelope, never the text it carries.
func checkRowValues(value reflect.Value) error {
	switch value.Kind() {
	case reflect.String:
		return rowValueText(value.String())
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return checkRowValues(value.Elem())
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := checkRowValues(iter.Key()); err != nil {
				return err
			}
			if err := checkRowValues(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := checkRowValues(value.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := checkRowValues(value.Field(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// encodeRowValues preserves the JSON batch envelope and its opaque text (S2).
func encodeRowValues(value any) (string, error) {
	if err := checkRowValues(reflect.ValueOf(value)); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	return string(data), err
}

// decodeRowValues is the single event/receipt value boundary (S2). It retains
// the existing JSON envelope and refuses damaged UTF-8 before JSON replaces it.
func decodeRowValues(data []byte, value any) error {
	if err := rowValueText(string(data)); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
