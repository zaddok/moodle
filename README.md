# Moodle API for Golang

This is a simple Moodle API that wraps the Moodle JSON Web Service API. 
It is part of a project that is currently in use in a live production environment, and
is under active development.

No warranty is given or implied. Use at your own risk.


## Example usage

	// Setup
	api := moodle.NewMoodleApi("https://moodle.example.com/moodle/", "a0092ba9a9f5b45cdd2f01d049595bfe91", l)

	// Search moodle courses
	courses, _ := api.GetCourses("History")
	if courses != nil {
		for _, i := range *courses {
			fmt.Printf("%s\n", i.Code)
		}
	}

	// Search users	
	people, err := api.GetPeopleByAttribute("email", "%")
	if err != nil {
		l.Error("%v", err)
		return
	}
	fmt.Println("People:")
	for _, p := range *people {
		// Do something
	}

