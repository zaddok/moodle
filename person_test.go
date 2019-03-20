package moodle

import (
	"fmt"
	"testing"
)

func TestPersonModule(t *testing.T) {

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	people, err := api.GetPeopleByAttribute("email", "%planetshakersc%")
	if err != nil {
		fmt.Printf("%v", err)
		return
	}
	if people == nil {
		fmt.Printf("people not returned\n")
		return
	}
	for _, p := range *people {
		fmt.Println(p.Username, p.FirstName, p.LastName, p.ProfileImageUrl)
	}

	fmt.Printf("Found %d people\n", len(*people))

	id, err := api.AddUser("FirstName", "LastName", "test@montebo.com", "test001", "tEst001!984-abc")
	fmt.Printf("%d %v\n", id, err)
	err = api.UpdateUser(id, "Mont", "ebo", "test@montebo.com", "test001", "")
	fmt.Printf("%v\n", err)
}

func TestPersonFields(t *testing.T) {

	person := Person{FirstName: "Bob"}
	person.SetField("a", "1")
	person.SetField("b", "2")
	person.SetField("c", "3")
	person.SetField("a", "5")

	if len(person.CustomField) != 3 {
		t.Errorf("CustomFields should have three items, not %d", len(person.CustomField))
	}

	if person.Field("a") != "5" {
		t.Errorf("CustomFields \"a\" should have value \"5\"  not %s", person.Field("a"))
	}

	if person.Field("b") != "2" {
		t.Errorf("CustomFields \"a\" should have value \"5\"  not %s", person.Field("a"))
	}

}
