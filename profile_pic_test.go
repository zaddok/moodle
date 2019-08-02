package moodle

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

func TestProfilePicUpload(t *testing.T) {

	file, err := os.Open("profile.jpg")
	if err != nil {
		fmt.Printf("%v", err)
		return
	}
	r := bufio.NewReader(file)

	api := NewMoodleApi(requireEnv("MOODLE_URL", t), requireEnv("MOODLE_KEY", t))
	api.SetLogger(&PrintMoodleLogger{})

	err = api.SetProfilePicture(2, r)
	if err != nil {
		fmt.Printf("%v", err)
		return
	}

}
