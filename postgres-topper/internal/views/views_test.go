package views

import (
	"strings"
	"testing"
)

func TestSpecsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range All {
		if v.Name == "" || seen[v.Name] {
			t.Errorf("view name %q empty or duplicated", v.Name)
		}
		seen[v.Name] = true
		if (v.FromTable == "") == (v.SQL == "") {
			t.Errorf("view %q must set exactly one of FromTable and SQL", v.Name)
		}
		if len(v.Tiebreak) == 0 {
			t.Errorf("view %q has no tiebreak; paging would be unstable", v.Name)
		}
		for _, denied := range []string{"encrypted_access_token", "key_version"} {
			if strings.Contains(v.SQL, denied) {
				t.Errorf("view %q selects %s", v.Name, denied)
			}
		}
		if strings.Contains(v.SQL, "SELECT *\n") || strings.HasPrefix(strings.TrimSpace(v.SQL), "SELECT *") {
			t.Errorf("view %q uses SELECT * at the top level; list columns so a new plaidsync column cannot leak", v.Name)
		}
	}
}
