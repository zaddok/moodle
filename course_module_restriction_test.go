package moodle

import (
	"fmt"
	"os"
	"testing"
)

func TestRestriction(t *testing.T) {

	// Simply a basic: Must be in audit group
	//     {"op":"&","c":[{"type":"group","id":191}],"showc":[true]}
	rules := &Restriction{}
	rules.OP = "&"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.ShowC = append(rules.ShowC, true)

	groups := []CourseGroup{CourseGroup{Id: 10}, CourseGroup{Id: 20}}
	rules.IsRestricted(groups)
	if rules.IsRestricted(groups) { // Should see (not restricted)
		t.Errorf("Restriction rule failure: %v %v", rules, groups)
	}

	groups = []CourseGroup{CourseGroup{Id: 5}, CourseGroup{Id: 15}}
	rules.IsRestricted(groups)
	if !rules.IsRestricted(groups) { // Should not see (restricted)
		t.Errorf("Restriction rule failure: %v %v", rules, groups)
	}

	//Must be in both groups:
	//  {"op":"&","c":[{"type":"group","id":191},{"type":"group","id":192}],"showc":[true,true]}"
	rules = &Restriction{}
	rules.OP = "&"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 20})
	rules.ShowC = append(rules.ShowC, true)
	rules.ShowC = append(rules.ShowC, true)

	//Must not be in audit group:
	//  {"op":"!&","c":[{"type":"group","id":191}],"show":true}
	rules = &Restriction{}
	rules.OP = "!&"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.Show = true

	//Must be in either group:
	//  {"op":"|","c":[{"type":"group","id":191},{"type":"group","id":192}],"show":true}
	rules = &Restriction{}
	rules.OP = "|"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 20})
	rules.ShowC = append(rules.ShowC, true)
	rules.ShowC = append(rules.ShowC, true)

	//Must not be in both groups:
	//  {"op":"!&","c":[{"type":"group","id":191},{"type":"group","id":192}],"show":true}
	rules = &Restriction{}
	rules.OP = "!&"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 20})
	rules.Show = true

	//Must not be in 191, or must not be in 192:
	//  {"op":"!|","c":[{"type":"group","id":191},{"type":"group","id":192}],"showc":[true,true]}
	rules = &Restriction{}
	rules.OP = "!|"
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 10})
	rules.C = append(rules.C, RestrictionC{Type: "group", Id: 20})
	rules.ShowC = append(rules.ShowC, true)
	rules.ShowC = append(rules.ShowC, true)

	//Must not be in any of these groups
	//  {"op":"!&","c":[{"type":"group","id":191},{"type":"group","id":192},{"type":"date","d":">=","t":1541682000}],"show":true}"

}

func requireEnv(name string, t *testing.T) string {
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf(fmt.Sprintf("Environment variable required: %s", name))
	}
	return value
}
