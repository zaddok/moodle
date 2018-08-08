// API for querying and updating a moodle server
//
//        api := moodle.NewMoodleApi("https://moodle.example.com/moodle/", "a0092ba9a9f5b45cdd2f01d049595bfe91", l)
//
//        // Search moodle courses
//        courses, _ := api.GetCourses("History")
//        if courses != nil {
//                for _, i := range *courses {
//                        fmt.Printf("%s\n", i.Code)
//                }
//        }
//
//        // Search users
//        people, err := api.GetPeopleByAttribute("email", "%")
//        if err != nil {
//                l.Error("%v", err)
//                return
//        }
//        fmt.Println("People:")
//        for _, p := range *people {
//                // Do something
//        }
//
package moodle

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"sort"
	"strings"
	"time"
)

// API Documentation
// https://docs.moodle.org/dev/Web_service_API_functions

type MoodleApi struct {
	base  string
	token string

	smtpUser      string
	smtpPassword  string
	smtpHost      string
	smtpPort      int
	smtpFromName  string
	smtpFromEmail string
}

func NewMoodleApi(base string, token string) *MoodleApi {
	return &MoodleApi{
		base:  base,
		token: token,
	}
}

func (m *MoodleApi) SetSmtpSettings(host string, port int, user, password string, fromName, fromEmail string) {
	m.smtpUser = user
	m.smtpPassword = password
	m.smtpHost = host
	m.smtpPort = port
	m.smtpFromName = fromName
	m.smtpFromEmail = fromEmail
}

type Course struct {
	MoodleId    int64         `json:"id,omitempty"`
	Code        string        `json:"shortname,omitempty"`
	Name        string        `json:"fullname,omitempty"`
	Summary     string        `json:",omitempty"`
	Assignments []*Assignment `json:",omitempty"`
	Roles       []*Role       `json:",omitempty"`
	Created     *time.Time    `json:"-"`
	Start       *time.Time    `json:",omitempty"`
	End         *time.Time    `json:",omitempty"`
}

type Person struct {
	MoodleId      int64      `json:",omitempty"`
	AlphacrucisId string     `json:",omitempty"`
	Username      string     `json:",omitempty"`
	Email         string     `json:",omitempty"`
	PersonalEmail string     `json:",omitempty"`
	FirstName     string     `json:",omitempty"`
	LastName      string     `json:",omitempty"`
	Created       *time.Time `json:",omitempty"`
	Roles         []*Role    `json:"role,omitempty"`
}

type Role struct {
	Person             *Person `json:",omitempty"`
	Course             *Course `json:",omitempty"`
	Role               *RoleInfo
	Enrolled           *time.Time
	GradeInfo          []GradeInfo `json:",omitempty"`
	GradeOverride      bool
	GradeOverrideValue float64
	GradeFinal         float64
}

type Submission struct {
	MoodleId  int64
	Person    Person
	Submitted *time.Time
	Extension *time.Time
}

type Assignment struct {
	MoodleId    int64        `json:",omitempty"`
	Name        string       `json:",omitempty"`
	Due         *time.Time   `json:",omitempty"`
	Weight      float64      `json:",omitempty"`
	Description string       `json:",omitempty"`
	Submissions []Submission `json:",omitempty"`
	Type        string       `json:",omitempty"`
	Updated     *time.Time   `json:",omitempty"`
}

type RoleInfo struct {
	Name     string `json:",omitempty"`
	MoodleId int64  `json:"-"`
}

type GradeInfo struct {
	Grade      float64     `json:",omitempty"`
	GradeMin   float64     `json:",omitempty"`
	GradeMax   float64     `json:",omitempty"`
	Assignment *Assignment `json:",omitempty"`
	Excluded   bool
	Updated    *time.Time `json:",omitempty"`
}

type ByCourseCode []Course

func (a ByCourseCode) Len() int      { return len(a) }
func (a ByCourseCode) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByCourseCode) Less(i, j int) bool {
	return a[i].Code < a[j].Code
}

func readError(body string) string {
	if !strings.HasPrefix(body, "{\"exception\":\"") || strings.Index(body, "\"message\":\"") < 0 {
		return ""
	}

	type Response struct {
		Message   string `json:"message"`
		Exception string `json:"exception"`
		ErrorCode string `json:"errorcode"`
		DebugInfo string `json:"debuginfo"`
	}
	var response Response
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return ""
	}

	if response.Message != "" {
		return response.Message
	}
	return response.Exception

}

// Get Moodle Account details matching by username. Returns nil if not found. Returns error if multiple matches are found.
func (m *MoodleApi) GetPersonByUsername(username string) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=username&values[0]=%s", m.base, m.token, "core_user_get_users_by_field",
		url.QueryEscape(username))
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	if len(people) == 0 {
		return nil, nil
	}
	if len(people) == 1 {
		return &people[0], nil
	}

	return nil, errors.New("Multiple moodle accounts match this username")
}

// Get Moodle Account details matching by moodle id. Returns nil if not found.
func (m *MoodleApi) GetPersonByMoodleId(id int64) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=id&values[0]=%d", m.base, m.token, "core_user_get_users_by_field",
		id)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	if len(people) == 0 {
		return nil, nil
	}
	if len(people) == 1 {
		return &people[0], nil
	}

	return nil, errors.New("Multiple moodle accounts match this username")
}

// Set the password for a moodle account. Password must match moodle password policy.
func (m *MoodleApi) ResetPassword(moodleId int64, password string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][password]=%s", m.base, m.token, "core_user_update_users", moodleId,
		url.QueryEscape(password))
	body, err := GetUrl(url)

	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	if strings.TrimSpace(body) != "null" {
		return errors.New("Server returned unexpected response: " + body)
	}

	return nil
}

// Get moodle account matching by email address.
func (m *MoodleApi) GetPersonByEmail(email string) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=email&values[0]=%s", m.base, m.token, "core_user_get_users_by_field",
		url.QueryEscape(email))
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	if len(people) == 0 {
		return nil, nil
	}
	if len(people) == 1 {
		return &people[0], nil
	}

	return nil, errors.New("Multiple moodle accounts match this email address")
}

func RandomLetter() string {
	bytes := make([]byte, 1)
	bytes[0] = byte(65 + rand.Intn(90-65))
	return string(bytes)
}

func RandomPassword() string {
	return fmt.Sprintf("%s%s-%d%s%s", RandomString(4), RandomLetter(), rand.Intn(9), RandomString(4), strings.ToLower(RandomLetter()))
}

// Reset the password for a moodle account, and email the password to the user
func (m *MoodleApi) ResetPasswordWithEmail(email string) error {
	p, err := m.GetPersonByEmail(email)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("Email address not found in moodle")
	}

	pwd := RandomPassword()
	err = m.ResetPassword(p.MoodleId, pwd)
	if err != nil {
		return errors.New("Password Reset failed. " + err.Error())
	}

	if m.smtpHost == "" || m.smtpPort == 0 {
		return errors.New("ResetPasswordWithEmail() requires smtp host and port to be specified.")
	}
	if m.smtpUser == "" || m.smtpPassword == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp user and password to be specified.")
	}
	if m.smtpFromName == "" || m.smtpFromEmail == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp from name and email to be specified.")
	}

	var w bytes.Buffer
	w.Write([]byte(fmt.Sprintf("From: %s <%s>\r\n", m.smtpFromName, m.smtpFromEmail)))
	w.Write([]byte(fmt.Sprintf("To: %s\r\n", p.FirstName+" "+p.LastName+" <"+p.Email+">")))
	w.Write([]byte(fmt.Sprintf("Subject: Welcome to the Planetshakers College moodle\r\n")))
	w.Write([]byte("Content-Type: text/plain; charset=utf-8; format=flowed\r\n"))
	w.Write([]byte("Content-Transfer-Encoding: 8bit\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Hi " + p.FirstName + ",\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Welcome to the Planetshakers College Moodle, You can sign-in using the details below:\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("    URL: " + m.base + "\r\n"))
	w.Write([]byte("    Username: " + p.Email + "\r\n"))
	w.Write([]byte("    Password: " + pwd + "\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("If you have any difficulties with moodle access, please contact college@planetshakers.com\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("God bless,\r\n"))
	w.Write([]byte("Planetshakers College\r\n"))
	w.Write([]byte("\r\n"))
	msg := w.Bytes()
	fmt.Println(string(msg))

	var auth smtp.Auth
	if m.smtpUser != "" && m.smtpPassword != "" {
		auth = smtp.PlainAuth("", m.smtpUser, m.smtpPassword, m.smtpHost)
	}

	// TLS config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         m.smtpHost,
	}

	// Here is the key, you need to call tls.Dial instead of smtp.Dial
	// for smtp servers running on 465 that require an ssl connection
	// from the very beginning (no starttls)
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", m.smtpHost, m.smtpPort), tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, m.smtpHost)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(m.smtpFromEmail); err != nil {
		return err
	}

	if err = c.Rcpt(p.Email); err != nil {
		return err
	}

	w1, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w1.Write([]byte(msg))
	if err != nil {
		return err
	}

	err = w1.Close()
	if err != nil {
		return err
	}

	c.Quit()

	return nil
}

// Reset the password for a moodle account, and email the password to the user
func (m *MoodleApi) WritingResetPasswordWithEmail(email string) error {
	p, err := m.GetPersonByEmail(email)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("Email address not found in moodle")
	}

	pwd := RandomPassword()
	err = m.ResetPassword(p.MoodleId, pwd)
	if err != nil {
		return err
	}

	if m.smtpHost == "" || m.smtpPort == 0 {
		return errors.New("ResetPasswordWithEmail() requires smtp host and port to be specified.")
	}
	if m.smtpUser == "" || m.smtpPassword == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp user and password to be specified.")
	}
	if m.smtpFromName == "" || m.smtpFromEmail == "" {
		return errors.New("ResetPasswordWithEmail() requires smtp from name and email to be specified.")
	}

	var w bytes.Buffer
	w.Write([]byte(fmt.Sprintf("From: %s <%s>\r\n", m.smtpFromName, m.smtpFromEmail)))
	w.Write([]byte(fmt.Sprintf("To: %s\r\n", p.FirstName+" "+p.LastName+" <"+p.Email+">")))
	w.Write([]byte(fmt.Sprintf("Subject: Welcome to RES101\r\n")))
	w.Write([]byte("Content-Type: text/plain; charset=utf-8; format=flowed\r\n"))
	w.Write([]byte("Content-Transfer-Encoding: 8bit\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Hi " + p.FirstName + ",\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("Welcome to the Planetshakers College Moodle, You now have access to RES101 in\r\n"))
	w.Write([]byte("Moodle. You can sign-in using the details below:\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("    URL: " + m.base + "\r\n"))
	w.Write([]byte("    Username: " + p.Email + "\r\n"))
	w.Write([]byte("    Password: " + pwd + "\r\n"))
	w.Write([]byte("\r\n"))
	w.Write([]byte("God bless,\r\n"))
	w.Write([]byte("Planetshakers College\r\n"))
	w.Write([]byte("\r\n"))
	msg := w.Bytes()
	fmt.Println(string(msg))

	var auth smtp.Auth
	if m.smtpUser != "" && m.smtpPassword != "" {
		auth = smtp.PlainAuth("", m.smtpUser, m.smtpPassword, m.smtpHost)
	}

	// TLS config
	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         m.smtpHost,
	}

	// Here is the key, you need to call tls.Dial instead of smtp.Dial
	// for smtp servers running on 465 that require an ssl connection
	// from the very beginning (no starttls)
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", m.smtpHost, m.smtpPort), tlsconfig)
	if err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, m.smtpHost)
	if err != nil {
		return err
	}

	if err = c.Auth(auth); err != nil {
		return err
	}

	if err = c.Mail(m.smtpFromEmail); err != nil {
		return err
	}

	if err = c.Rcpt(p.Email); err != nil {
		return err
	}

	w1, err := c.Data()
	if err != nil {
		return err
	}

	_, err = w1.Write([]byte(msg))
	if err != nil {
		return err
	}

	err = w1.Close()
	if err != nil {
		return err
	}

	c.Quit()

	return nil
}

// Fetch moodle accounts that match match by first and last name.
func (m *MoodleApi) GetPeopleByFirstNameLastName(firstname, lastname string) (*[]Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criteria[0][key]=firstname&criteria[0][value]=%s&criteria[0][key]=lastname&criteria[0][value]=%s", m.base, m.token, "core_user_get_users",
		url.QueryEscape(firstname),
		url.QueryEscape(lastname))
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}
	type Results struct {
		People []Result `json:"users"`
		Total  int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results.People))
	for _, i := range results.People {
		if strings.ToLower(i.FirstName) == strings.ToLower(firstname) &&
			strings.ToLower(i.LastName) == strings.ToLower(lastname) {
			people = append(people, Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username})
		}
	}

	return &people, nil
}

// Fetch moodle accounts that have a specific field. For example: api.GetPersonByAttribute("firstname", "James")
func (m *MoodleApi) GetPeopleByAttribute(attribute, value string) (*[]Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criteria[0][key]=%s&criteria[0][value]=%s", m.base, m.token, "core_user_get_users",
		url.QueryEscape(attribute),
		url.QueryEscape(value))
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	type Result struct {
		Id           int64         `json:"id"`
		FirstName    string        `json:"firstname"`
		LastName     string        `json:"lastname"`
		Email        string        `json:"email"`
		Username     string        `json:"username"`
		CustomFields []CustomField `json:"customfields"`
	}
	type Results struct {
		People []Result `json:"users"`
		Total  int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results.People))
	for _, i := range results.People {
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			if c.Name == "alphacrucisid" {
				p.AlphacrucisId = c.Value
			}
			if c.Name == "personalemail" {
				p.PersonalEmail = c.Value
			}
		}
		people = append(people, p)
	}

	return &people, nil
}

// Moodle's bug causes role_id to be ignored: https://tracker.moodle.org/browse/MDL-51152
func (m *MoodleApi) UnsetRole(personId int64, roleId int64, courseId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&enrolments[0][roleid]=%d&enrolments[0][userid]=%d&enrolments[0][courseid]=%d", m.base, m.token, "enrol_manual_unenrol_users", roleId, personId, courseId)

	body, err := GetUrl(url)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	return nil
}

func (m *MoodleApi) SetRole(personId int64, roleId int64, courseId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&enrolments[0][roleid]=%d&enrolments[0][userid]=%d&enrolments[0][courseid]=%d", m.base, m.token, "enrol_manual_enrol_users", roleId, personId, courseId)

	body, err := GetUrl(url)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	return nil
}

func (m *MoodleApi) SetUserAttribute(personId int64, attribute, value string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][%s]=%s", m.base, m.token, "core_user_update_users", personId,
		url.QueryEscape(attribute),
		url.QueryEscape(value),
	)

	body, err := GetUrl(url)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	if strings.TrimSpace(body) != "" {
		return errors.New("Server returned unexpected response: " + body)
	}

	return nil
}

func (m *MoodleApi) SetUserCustomField(personId int64, attribute, value string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][customfields][0][type]=%s&users[0][customfields][0][value]=%s", m.base, m.token, "core_user_update_users", personId,
		url.QueryEscape(attribute),
		url.QueryEscape(value),
	)

	body, err := GetUrl(url)

	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	if strings.TrimSpace(body) != "" {
		return errors.New("Server returned unexpected response: " + body)
	}

	return nil
}

func (m *MoodleApi) RemovePersonFromCourseGroup(personId int64, groupId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&members[0][userid]=%d&members[0][groupid]=%d", m.base, m.token, "core_group_delete_group_members", personId, groupId)

	body, err := GetUrl(url)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	if strings.TrimSpace(body) != "null" {
		return errors.New("Server returned unexpected response: " + body + "--" + url)
	}

	return nil
}

func (m *MoodleApi) AddPersonToCourseGroup(personId int64, groupId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&members[0][userid]=%d&members[0][groupid]=%d", m.base, m.token, "core_group_add_group_members", personId, groupId)

	body, err := GetUrl(url)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	if strings.TrimSpace(body) != "null" {
		return errors.New("Server returned unexpected response: " + body + "--" + url)
	}

	return nil
}

func (m *MoodleApi) AddUser(firstName, lastName, email, username, password string) (int64, error) {

	if strings.Index(email, "@") < 0 {
		return 0, errors.New("Invalid email address")
	}

	var l string
	if password == "" {
		l = fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][firstname]=%s&users[0][lastname]=%s&users[0][email]=%s&users[0][username]=%s&users[0][createpassword]=1", m.base, m.token, "core_user_create_users",
			url.QueryEscape(firstName),
			url.QueryEscape(lastName),
			url.QueryEscape(email),
			url.QueryEscape(username))
	} else {
		l = fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][firstname]=%s&users[0][lastname]=%s&users[0][email]=%s&users[0][username]=%s&users[0][password]=%s", m.base, m.token, "core_user_create_users",
			url.QueryEscape(firstName),
			url.QueryEscape(lastName),
			url.QueryEscape(email),
			url.QueryEscape(username),
			url.QueryEscape(password))
	}
	//fmt.Println(l)

	body, err := GetUrl(l)
	fmt.Println(body)
	if err != nil {
		return 0, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return 0, errors.New(message + ". " + l)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	var data []map[string]interface{}

	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return 0, errors.New("Server returned unexpected response. " + err.Error())
	}
	if len(data) != 1 {
		return 0, errors.New("Server returned unexpected response. " + err.Error())
	}
	if _, ok := data[0]["id"]; !ok {
		return 0, errors.New("Server returned unexpected response. ID is missing. " + err.Error())
	}

	return int64(data[0]["id"].(float64)), nil
}

type CourseGroup struct {
	Id        int64  `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortname"`
}

func (m *MoodleApi) GetPersonCourseList(userId int64) (*[]Course, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&userid=%d", m.base, m.token, "core_enrol_get_users_courses", userId)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	var results []Course

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return &results, nil
}

// List the details of each group in a course. Fetches: id, name, and shortname
func (m *MoodleApi) GetCourseGroups(courseId int64) (*[]CourseGroup, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_group_get_course_groups", courseId)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	var results []CourseGroup

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return &results, nil
}

type CustomField struct {
	Name  string `json:"shortname"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type CoursePerson struct {
	Id           int64         `json:"id"`
	Username     string        `json:"username"`
	FirstName    string        `json:"firstname"`
	LastName     string        `json:"lastname"`
	Email        string        `json:"email"`
	LastAccess   int64         `json:"lastaccess"`
	FirstAccess  int64         `json:"firstaccess"`
	Groups       []CourseGroup `json:"groups"`
	Roles        []CourseGroup `json:"roles"`
	CustomFields []CustomField `json:"customfields"`
}

func (cp *CoursePerson) FirstAccessTime() *time.Time {
	if cp.FirstAccess == 0 {
		return nil
	}
	t := time.Unix(cp.FirstAccess, 0)
	return &t
}

func (cp *CoursePerson) LastAccessTime() *time.Time {
	if cp.LastAccess == 0 {
		return nil
	}
	t := time.Unix(cp.LastAccess, 0)
	return &t
}

func (cp *CoursePerson) CustomField(name string) string {
	for _, i := range cp.CustomFields {
		if name == i.Name {
			return i.Value
		}
	}
	return ""
}

func (cp *CoursePerson) HasGroupNamed(name string) bool {
	for _, i := range cp.Groups {
		if name == i.Name {
			return true
		}
	}
	return false
}

// List all people in a course. Results include the persons roles and groups
func (m *MoodleApi) GetCourseRoles(courseId int64) (*[]CoursePerson, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_enrol_get_enrolled_users", courseId)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	var results []CoursePerson

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return &results, nil
}

func (m *MoodleApi) GetCourses(value string) (*[]Course, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criterianame=search&criteriavalue=%s", m.base, m.token, "core_course_search_courses", value)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id   int64  `json:"id"`
		Code string `json:"shortname"`
		Name string `json:"fullname"`
	}
	type Results struct {
		Courses []Result `json:"courses"`
		Total   int64    `json:"total"`
	}

	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	subjects := make([]Course, 0, len(results.Courses))
	for _, i := range results.Courses {
		subjects = append(subjects, Course{MoodleId: i.Id, Code: i.Code, Name: i.Name})
	}
	sort.Sort(ByCourseCode(subjects))

	return &subjects, nil
}

func (m *MoodleApi) GetSiteInfo() (string, string, string, int64, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "core_webservice_get_site_info")

	body, err := GetUrl(url)

	if err != nil {
		return "", "", "", 0, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return "", "", "", 0, errors.New(body)
	}

	type SiteInfo struct {
		Sitename  string
		Firstname string
		Lastname  string
		Userid    int64
	}

	var data map[string]interface{}

	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return "", "", "", 0, errors.New("Server returned unexpected response. " + err.Error())
	}

	return data["sitename"].(string), data["firstname"].(string), data["lastname"].(string), int64(data["userid"].(float64)), nil
}

func GetUrl(url string) (string, error) {

	timeout := time.Duration(25 * time.Second)
	client := http.Client{
		Transport: &http.Transport{
			Dial: func(netw, addr string) (net.Conn, error) {
				deadline := time.Now().Add(15 * time.Second)
				c, err := net.DialTimeout(netw, addr, time.Second*5)
				if err != nil {
					return nil, err
				}
				c.SetDeadline(deadline)
				return c, nil
			},
		},
		Timeout: timeout,
	}

	res, err := client.Get(url)
	if err != nil {
		if strings.Contains(err.Error(), "dial tcp: i/o timeout") {
			return "", errors.New("Timout connecting to server")
		}
		if strings.Contains(err.Error(), "use of closed network connection") {
			return "", errors.New("Timout waiting for server")
		}
		return "", err
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return string(body), err
	}

	return string(body), err
}

type AssignmentInfo struct {
	Id         int64      `json:"id"`
	CmId       int64      `json:"cmid"`
	CourseId   int64      `json:"courseid"`
	CourseCode string     `json:"coursecode"`
	CourseName string     `json:"coursename"`
	Name       string     `json:"name"`
	DueDate    *time.Time `json:"duedate"`
}

func (m *MoodleApi) GetAssignments(courses *[]Course) (*[]*AssignmentInfo, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&includenotenrolledcourses=1", m.base, m.token, "mod_assign_get_assignments")
	for i, c := range *courses {
		url = fmt.Sprintf("%s&courseids%%5B%d%%5D=%d", url, i, c.MoodleId)
	}
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type AssignInfo struct {
		Id      int64  `json:"id"`
		CmId    int64  `json:"cmid"`
		Name    string `json:"name"`
		DueDate int64  `json:"duedate"`
		//NoSubmissions bool   `json:"nosubmissions"`
	}

	type CourseAssign struct {
		Id          int64        `json:"id"`
		Code        string       `json:"shortname"`
		Name        string       `json:"fullname"`
		Assignments []AssignInfo `json:"assignments"`
	}

	type Result struct {
		Courses []CourseAssign `json:"courses"`
	}

	var results Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	assignments := make([]*AssignmentInfo, 0)
	for _, c := range results.Courses {
		for _, a := range c.Assignments {
			var t *time.Time
			if a.DueDate != 0 {
				tt := time.Unix(a.DueDate, 0)
				t = &tt
			}
			ai := &AssignmentInfo{Id: a.Id, CmId: a.CmId, Name: a.Name, CourseCode: c.Code, CourseName: c.Name, CourseId: c.Id, DueDate: t}
			assignments = append(assignments, ai)
		}
	}

	return &assignments, nil
}

type AssignmentSubmission struct {
	Id            int64  `json:"id"`
	SubmissionId  int64  `json:"submissionid"`
	UserId        int64  `json:"userid"`
	Status        string `json:"status"`
	GradingStatus string `json:"gradingstatus"`
}

func (m *MoodleApi) GetAssignmentSubmissions(assignmentId int64) (*[]AssignmentSubmission, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&assignmentids[0]=%d", m.base, m.token, "mod_assign_get_submissions", assignmentId)
	body, err := GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Plugin struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}

	type AssignSub struct {
		Id            int64    `json:"id"`
		UserId        int64    `json:"userid"`
		Status        string   `json:"status"`
		GradingStatus string   `json:"gradingstatus"`
		Plugins       []Plugin `json:"plugins"`
	}

	type Assign struct {
		Id          int64       `json:"assignmentid"`
		Submissions []AssignSub `json:"submissions"`
	}

	type Result struct {
		Assignments []Assign `json:"assignments"`
	}

	var results Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	assignments := make([]AssignmentSubmission, 0)
	for _, k := range results.Assignments {
		for _, i := range k.Submissions {
			assignments = append(assignments, AssignmentSubmission{Id: k.Id, SubmissionId: i.Id, UserId: i.UserId, Status: i.Status, GradingStatus: i.GradingStatus})
			//fmt.Println(i)
		}
	}

	return &assignments, nil
}

func GetAttendance() error {

	// Get attendance for a session

	// But how to we know which sessions to look at?

	return nil
}
