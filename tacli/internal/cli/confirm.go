package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// requireConfirmation prints a warning and requires the user to type a
// randomly generated hex code before proceeding. Returns an error if the
// user aborts or types the wrong code.
//
// warning is shown as the first line (e.g. "This will delete the kernel").
// details are optional bullet lines shown below the warning.
func requireConfirmation(warning string, details ...string) error {
	code := make([]byte, 4)
	if _, err := rand.Read(code); err != nil {
		return fmt.Errorf("generate confirmation code: %w", err)
	}
	confirm := hex.EncodeToString(code)

	fmt.Printf("⚠  %s\n", warning)
	for _, d := range details {
		fmt.Printf("   %s\n", d)
	}
	fmt.Println()
	fmt.Printf("   Type %s to confirm: ", confirm)

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("aborted")
	}
	if strings.TrimSpace(scanner.Text()) != confirm {
		return fmt.Errorf("confirmation code did not match — aborted")
	}
	return nil
}
