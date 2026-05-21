package rates

import "os"

func writeStr(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
