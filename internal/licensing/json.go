package licensing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func newReader(b []byte) io.Reader { return bytes.NewReader(b) }

// unmarshalLicense разбирает полезную нагрузку ключа, отвергая неизвестные поля:
// ключ подписан поставщиком, и расхождение версий формата должно быть видно сразу.
func unmarshalLicense(body []byte, l *License) error {
	dec := json.NewDecoder(newReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(l); err != nil {
		return fmt.Errorf("%w: разбор тела: %w", ErrInvalidKey, err)
	}
	return nil
}
