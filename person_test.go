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

}
