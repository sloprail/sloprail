package declaration

import (
	"strings"
	"testing"
)

// A file-guard whose match cannot compile is told what the scope holds — every
// variable, oldMarkers included, or an author reaching for the marker a write
// removes is sent looking for a name the message never mentions.
func TestValidateFileGuard_BadMatchNamesTheWholeScope(t *testing.T) {
	problems := ValidateFileGuard(FileGuard{Match: `any(markerz, .kind == "x")`}, Env{})
	for _, p := range problems {
		if p.Where == "match" {
			for _, v := range []string{"path", "markers", "oldMarkers", "trailers"} {
				if !strings.Contains(p.Detail, v) {
					t.Errorf("the match error does not name %q: %s", v, p.Detail)
				}
			}
			return
		}
	}
	t.Fatalf("a match naming an unknown variable was not refused: %+v", problems)
}
