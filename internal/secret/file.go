package secret

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const maxSecretSize = 64 << 10

func ReadFile(path, purpose string) (contents []byte, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s file %q: %w", purpose, path, err)
	}
	defer func() {
		if err := file.Close(); err != nil && resultErr == nil {
			resultErr = fmt.Errorf("close %s file %q: %w", purpose, path, err)
		}
	}()

	contents, err = io.ReadAll(io.LimitReader(file, maxSecretSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s file %q: %w", purpose, path, err)
	}
	if len(contents) > maxSecretSize {
		return nil, fmt.Errorf("%s file %q exceeds %d bytes", purpose, path, maxSecretSize)
	}

	contents = removeSingleTrailingNewline(contents)
	if len(contents) == 0 {
		return nil, fmt.Errorf("%s file %q is empty", purpose, path)
	}
	if bytes.IndexByte(contents, 0) >= 0 {
		return nil, fmt.Errorf("%s file %q contains a NUL byte", purpose, path)
	}

	return contents, nil
}

func removeSingleTrailingNewline(value []byte) []byte {
	if len(value) == 0 || value[len(value)-1] != '\n' {
		return value
	}

	value = value[:len(value)-1]
	if len(value) > 0 && value[len(value)-1] == '\r' {
		value = value[:len(value)-1]
	}

	return value
}
