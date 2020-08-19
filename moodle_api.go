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
	crand "crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/smtp"
	"net/url"
	"sort"
	"strconv"
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

	log   MoodleLogger
	fetch LookupUrl
}

func NewMoodleApi(base string, token string) *MoodleApi {
	if base != "" {
		if !strings.HasSuffix(base, "/") {
			base = base + "/"
		}
	}
	return &MoodleApi{
		base:  base,
		token: token,
		log:   &NilMoodleLogger{},
		fetch: &DefaultLookupUrl{},
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

func (m *MoodleApi) MoodleUrl() string {
	return m.base
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
	MoodleId             int64  `json:",omitempty"`
	Username             string `json:",omitempty"`
	Email                string `json:",omitempty"`
	FirstName            string `json:",omitempty"`
	LastName             string `json:",omitempty"`
	ProfileImageUrl      string `json:"profileimageurl,omitempty"`
	ProfileImageUrlSmall string `json:"profileimageurlsmall,omitempty"`
	Suspended            bool
	Created              *time.Time    `json:",omitempty"`
	Roles                []*Role       `json:"role,omitempty"`
	CustomField          []CustomField `json:"customfields,omitempty"`
}

func (p *Person) Field(name string) string {
	for _, c := range p.CustomField {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (p *Person) SetField(name, value string) {
	for i, c := range p.CustomField {
		if c.Name == name {
			p.CustomField[i].Value = value
			return
		}
	}
	p.CustomField = append(p.CustomField, CustomField{Name: name, Value: value})
	return
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
	body, _, _, err := m.fetch.GetUrl(url)
	m.log.Debug("Fetch: %s", url)

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
	if len(results) > 1 {
		return nil, errors.New("Multiple moodle accounts match this username")
	}

	var person *Person
	for _, i := range results {
		person = &Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			person.CustomField = append(person.CustomField, CustomField{Name: c.Name, Value: c.Value})
		}
		break
	}

	return person, nil
}

type MoodleLogger interface {
	Debug(message string, items ...interface{}) error
}

type NilMoodleLogger struct {
}

func (ml *NilMoodleLogger) Debug(message string, items ...interface{}) error {
	return nil
}

func (m *MoodleApi) SetLogger(l MoodleLogger) {
	m.log = l
}

// Get Moodle Account details matching by moodle id. Returns nil if not found.
func (m *MoodleApi) GetPersonByMoodleId(id int64) (*Person, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&field=id&values[0]=%d", m.base, m.token, "core_user_get_users_by_field",
		id)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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
	if len(results) > 1 {
		return nil, errors.New("Multiple moodle accounts match this username")
	}

	var person *Person
	for _, i := range results {
		person = &Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username}
		for _, c := range i.CustomFields {
			person.CustomField = append(person.CustomField, CustomField{Name: c.Name, Value: c.Value})
		}
		break
	}

	return person, nil
}

type UploadResponse struct {
	ItemId int64 `json:"itemid"`
}

// SetProfilePicture uploads a draft file, set is as a profile picture, then removes the draft file
func (m *MoodleApi) SetProfilePicture(userMoodleId int64, r io.Reader) error {
	now := time.Now()

	data, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}
	img := base64.StdEncoding.EncodeToString(data)
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&filearea=draft&instanceid=%d&component=user&filepath=/&contextlevel=user&filename=profilepic%s.jpg&filecontent=%s&itemid=%d", m.base, m.token, "core_files_upload", userMoodleId, now.Format("20060102150405"), url.QueryEscape(img), userMoodleId)

	// 1. Upload a draft file
	//url := fmt.Sprintf("%swebservice/upload.php?token=%s&wsfunction=%s&moodlewsrestformat=json&filearea=draft&instanceid=%d&component=user&filepath=/&contextlevel=user&filename=profilepic%s.jpg&itemid=%d", m.base, m.token, "core_files_upload", userMoodleId, now.Format("20060102150405"), userMoodleId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)
	if err != nil {
		return err
	}
	fmt.Println(body)
	var draftFileId int64 = 0
	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}
	if strings.Index(body, "\"itemid\":") > 0 {
		var u UploadResponse
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			return errors.New("Server returned unexpected response. " + err.Error())
		}
		draftFileId = u.ItemId
	} else {
		return errors.New("Server returned unexpected response: " + body)
	}
	fmt.Println(draftFileId)

	// 2. Update the profile picture
	url = fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&draftitemid=%d&userid=%d", m.base, m.token, "core_user_update_picture", draftFileId, userMoodleId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err = m.fetch.GetUrl(url)
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

	fmt.Println("profile set")

	// 3. Remove the draft file
	/*
		url = fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&draftitemid=0&delete=1", m.base, m.token, "core_user_update_picture")
		m.log.Debug("Fetch: %s", url)
		body, _, _, err = m.fetch.GetUrl(url)
		if err != nil {
			return err
		}
		if strings.HasPrefix(body, "{\"exception\":\"") {
			message := readError(body)
			return errors.New(message + ". " + url)
		}
		if strings.TrimSpace(body) != "null" {
			return errors.New("Server returned unexpected response: " + body)
		}*/

	return nil
}

// Set the password for a moodle account. Password must match moodle password policy.
func (m *MoodleApi) ResetPassword(moodleId int64, password string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][password]=%s", m.base, m.token, "core_user_update_users", moodleId,
		url.QueryEscape(password))
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	type Result struct {
		Id                   int64         `json:"id"`
		FirstName            string        `json:"firstname"`
		LastName             string        `json:"lastname"`
		Email                string        `json:"email"`
		Username             string        `json:"username"`
		ProfileImageUrl      string        `json:"profileimageurl,omitempty"`
		ProfileImageUrlSmall string        `json:"profileimageurlsmall,omitempty"`
		CustomFields         []CustomField `json:"customfields"`
	}

	var results []Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	people := make([]Person, 0, len(results))
	for _, i := range results {
		if strings.Index(i.ProfileImageUrl, "gravatar") > 0 {
			i.ProfileImageUrl = ""
			i.ProfileImageUrlSmall = ""
		}
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username, ProfileImageUrl: i.ProfileImageUrl, ProfileImageUrlSmall: i.ProfileImageUrlSmall}
		for _, c := range i.CustomFields {
			p.CustomField = append(p.CustomField, CustomField{Name: c.Name, Value: c.Value})
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

const rst = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

func NewCryptoSeededSource() rand.Source {
	var seed int64
	binary.Read(crand.Reader, binary.BigEndian, &seed)
	return rand.NewSource(seed)
}

// RandomPassword differs from RandomString in that it ensures we dont have
// a series of repeated or incrementing characters, and ensures we have at
// least one uppercase lowercase, and number character.
func RandomPassword() string {
	size := 10
	random := rand.New(NewCryptoSeededSource())

	bytes := make([]byte, size)
	hasNumber := false
	hasLowercase := false
	hasUppercase := false
	var last byte = 0
	for i := 0; i < size; i++ {
		if size > 2 && i == size-1 && hasUppercase == false {
			c := 'B' + uint8(random.Int31n(25))
			if c == 'O' {
				c = 'A'
			}
			bytes[i] = c
			continue
		}
		if size > 3 && i == size-2 && hasLowercase == false {
			c := 'b' + uint8(random.Int31n(25))
			if c == 'l' {
				c = 'a'
			}
			bytes[i] = c
			continue
		}
		if size > 4 && i == size-3 && hasNumber == false {
			c := '2' + uint8(random.Int31n(8))
			bytes[i] = c
			continue
		}

		x := random.Intn(len(rst))
		c := rst[x]

		if c == last || c == last+1 {
			i = i - 1
			continue
		}

		if c <= '9' {
			hasNumber = true
		} else if c <= 'Z' {
			hasUppercase = true
		} else {
			hasLowercase = true
		}

		bytes[i] = c
		last = c
	}
	s := string(bytes)
	return s[0:5] + "-" + s[5:]
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
		return errors.New(fmt.Sprintf("tls.Dial(\"%s:%d\") failed: %v", m.smtpHost, m.smtpPort, err))
	}

	c, err := smtp.NewClient(conn, m.smtpHost)
	if err != nil {
		return errors.New(fmt.Sprintf("SMTP.NewClient() failed: %v", err))
	}

	if err = c.Auth(auth); err != nil {
		return errors.New(fmt.Sprintf("SMTP.Auth() failed: %v", err))
	}

	if err = c.Mail(m.smtpFromEmail); err != nil {
		return errors.New(fmt.Sprintf("SMTP.Mail() failed: %v", err))
	}

	if err = c.Rcpt(p.Email); err != nil {
		return errors.New(fmt.Sprintf("SMTP.Rcpt() failed: %v", err))
	}

	w1, err := c.Data()
	if err != nil {
		return errors.New(fmt.Sprintf("SMTP.Data() failed: %v", err))
	}

	_, err = w1.Write([]byte(msg))
	if err != nil {
		return err
	}

	err = w1.Close()
	if err != nil {
		return errors.New(fmt.Sprintf("SMTP.Close() failed: %v", err))
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
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return nil, errors.New(message + ". " + url)
	}

	type Result struct {
		Id                   int64         `json:"id"`
		FirstName            string        `json:"firstname"`
		LastName             string        `json:"lastname"`
		Email                string        `json:"email"`
		Username             string        `json:"username"`
		ProfileImageUrl      string        `json:"profileimageurl,omitempty"`
		ProfileImageUrlSmall string        `json:"profileimageurlsmall,omitempty"`
		CustomFields         []CustomField `json:"customfields"`
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
		if strings.Index(i.ProfileImageUrl, "gravatar") > 0 {
			i.ProfileImageUrl = ""
			i.ProfileImageUrlSmall = ""
		}
		p := Person{MoodleId: i.Id, FirstName: i.FirstName, LastName: i.LastName, Email: i.Email, Username: i.Username, ProfileImageUrl: i.ProfileImageUrl, ProfileImageUrlSmall: i.ProfileImageUrlSmall}
		for _, c := range i.CustomFields {
			p.CustomField = append(p.CustomField, CustomField{Name: c.Name, Value: c.Value})
		}
		people = append(people, p)
	}

	return &people, nil
}

// Moodle's bug causes role_id to be ignored: https://tracker.moodle.org/browse/MDL-51152
func (m *MoodleApi) UnsetRole(personId int64, roleId int64, courseId int64) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&enrolments[0][roleid]=%d&enrolments[0][userid]=%d&enrolments[0][courseid]=%d", m.base, m.token, "enrol_manual_unenrol_users", roleId, personId, courseId)
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
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
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
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
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
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

// SetAssessmentExtensionDate sets a new due date for an assignment for
// a specific user. The userId parameter is the same ID that appears in the
// moodle URL when viewing a user. The assessmentId is not the same ID as the
// ID shown in a URL when viewing an assessment, it is the ID from the
// mdl_assign table. This API updates the mdl_assign_user_flags database
// table.
func (m *MoodleApi) SetAssessmentExtensionDate(userId, assessmentId int64, newDueDate time.Time) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&assignmentid=%d&userflags[0][userid]=%d&userflags[0][extensionduedate]=%d", m.base, m.token,
		"mod_assign_set_user_flags",
		assessmentId,
		userId,
		newDueDate.Unix())
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + url)
	}

	if strings.HasPrefix(strings.TrimSpace(body), "[{") && strings.Index(body, "\"id\":") > 0 {
		return nil
	}

	return errors.New("Server returned unexpected response: " + body)
}

func (m *MoodleApi) SetUserCustomField(personId int64, attribute, value string) error {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][customfields][0][type]=%s&users[0][customfields][0][value]=%s", m.base, m.token, "core_user_update_users", personId,
		url.QueryEscape(attribute),
		url.QueryEscape(value),
	)
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)

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
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
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
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
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

func (m *MoodleApi) AddGroupToCourse(courseId int64, groupName, groupDescription string) (int64, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&groups[0][courseid]=%d&groups[0][name]=%s&groups[0][description]=%s", m.base, m.token, "core_group_create_groups", courseId, url.QueryEscape(groupName), url.QueryEscape(groupDescription))
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)
	if err != nil {
		return 0, err
	}
	if body == "" {
		return 0, errors.New("Moodle returned no response")
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return 0, errors.New(message + ". " + url)
	}

	type GroupInfo struct {
		Id          int64
		Courseid    int64
		Name        string
		Description string
		Idnumber    string
	}

	var response []GroupInfo

	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return 0, errors.New("Moodle returned unexpected response. " + err.Error())
	}
	if len(response) != 1 {
		return 0, errors.New("Moodle returned unexpected response. " + err.Error())
	}

	return response[0].Id, nil

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
	m.log.Debug("Fetch: %s", l)

	body, _, _, err := m.fetch.GetUrl(l)
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

// UpdateUser updates the basic details of a moodle account. Requires permission for "core_user_update_users". Password is only updated if password is not blank.
func (m *MoodleApi) UpdateUser(id int64, firstName, lastName, email, username, password string) error {

	if strings.Index(email, "@") < 0 {
		return errors.New("Invalid email address")
	}

	var l string
	l = fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&users[0][id]=%d&users[0][firstname]=%s&users[0][lastname]=%s&users[0][email]=%s&users[0][username]=%s", m.base, m.token, "core_user_update_users", id,
		url.QueryEscape(firstName),
		url.QueryEscape(lastName),
		url.QueryEscape(email),
		url.QueryEscape(username))
	if password != "" {
		l = l + "&users[0][password]=" + url.QueryEscape(password)
	}
	//fmt.Println(l)
	m.log.Debug("Fetch: %s", l)

	body, _, _, err := m.fetch.GetUrl(l)
	fmt.Println(body)
	if err != nil {
		return err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		message := readError(body)
		return errors.New(message + ". " + l)
	}

	return nil
}

type CourseGroup struct {
	Id          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CourseRole struct {
	Id        int64  `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortname"`
}

func (m *MoodleApi) GetPersonCourseList(userId int64) ([]Course, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&userid=%d", m.base, m.token, "core_enrol_get_users_courses", userId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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

	return results[:], nil
}

// List the details of each group in a course. Fetches: id, name, and shortname
func (m *MoodleApi) GetCourseGroups(courseId int64) ([]CourseGroup, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_group_get_course_groups", courseId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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

	return results[:], nil
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
	Roles        []CourseRole  `json:"roles"`
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
	name = strings.ToLower(name)
	for _, i := range cp.Groups {
		if name == strings.ToLower(i.Name) || name == strings.ToLower(i.Description) {
			return true
		}
	}
	return false
}

func (cp *CoursePerson) HasRoleNamed(name string) bool {
	name = strings.ToLower(name)
	for _, i := range cp.Roles {
		if name == strings.ToLower(i.Name) || name == strings.ToLower(i.ShortName) {
			return true
		}
	}
	return false
}

type GradebookEntry struct {
	UserId   int64           `json:"userid"`
	Name     string          `json:"userfullname"`
	MaxDepth int64           `json:"maxdepth"`
	Item     []GradebookItem `json:"gradeitems"`
}

type GradebookItem struct {
	Id                  int64   `json:"id"`
	ItemName            string  `json:"itemname"`
	ItemType            string  `json:"itemtype"`
	ItemModule          string  `json:"itemmodule"`
	ItemInstance        int64   `json:"iteminstance"`
	ItemNumber          int64   `json:"itemnumber"`
	CategoryId          int64   `json:"categoryid"`
	OutcomeId           int64   `json:"outcomeid"`
	CmId                int64   `json:"cmid"`
	GradedDate          int64   `json:"gradedategraded"`
	GradeRaw            float64 `json:"graderaw"`
	GradeMax            float64 `json:"grademax"`
	GradeFormatted      string  `json:"gradeformatted"`
	GradeDateSubmitted  int64   `json:"gradedatesubmitted"`
	GradeDateGraded     int64   `json:"gradedategraded"`
	PercentageFormatted string  `json:"percentageformatted"`
	WeightRaw           float64 `json:"weightraw"`
	GradeIsHidden       bool    `json:"gradeishidden"`
}

func (i *GradebookItem) InferGrade() float64 {
	if i.GradeMax > 0 && i.GradeRaw > 0 {
		return i.GradeRaw / i.GradeMax * 100
	}
	if len(i.PercentageFormatted) > 0 && strings.HasSuffix(i.PercentageFormatted, "%") {
		v := i.PercentageFormatted[0 : len(i.PercentageFormatted)-1]
		v = strings.TrimSpace(v)
		r, _ := strconv.ParseFloat(v, 64)
		return r
	}
	return 0
}

func (e *GradebookItem) Submitted() *time.Time {
	if e.GradeDateSubmitted == 0 {
		return nil
	}
	t := time.Unix(e.GradeDateSubmitted, 0)
	return &t
}

func (e *GradebookItem) Graded() *time.Time {
	if e.GradeDateGraded == 0 {
		return nil
	}
	t := time.Unix(e.GradeDateGraded, 0)
	return &t
}

// List all gradebook data associated with a course.
func (m *MoodleApi) GetCourseGradebook(courseId int64) ([]GradebookEntry, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "gradereport_user_get_grade_items", courseId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Results struct {
		Usergrades []GradebookEntry `json:"usergrades"`
	}
	var results Results

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return results.Usergrades[:], nil
}

// List all people in a course. Results include the persons roles and groups
func (m *MoodleApi) GetCourseRoles(courseId int64) ([]CoursePerson, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&courseid=%d", m.base, m.token, "core_enrol_get_enrolled_users", courseId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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

	return results[:], nil
}

func (m *MoodleApi) GetCourses(value string) ([]Course, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&criterianame=search&criteriavalue=%s", m.base, m.token, "core_course_search_courses", url.QueryEscape(value))
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Id          int64  `json:"id"`
		Code        string `json:"shortname"`
		Name        string `json:"fullname"`
		DisplayName string `json:"displayname"`
		CategoryId  int64  `json:"categoryid"`
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

	return subjects[:], nil
}

func (m *MoodleApi) GetSiteInfo() (string, string, string, int64, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "core_webservice_get_site_info")
	m.log.Debug("Fetch: %s", url)

	body, _, _, err := m.fetch.GetUrl(url)

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

/*
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
*/

func (r *Restriction) IsRestricted(groups []CourseGroup) bool {
	switch r.OP {
	case "&":
		// Check user is in every group
		for _, r := range r.C {
			found := false
			for _, g := range groups {
				if r.Id == g.Id {
					found = true
				}
			}
			if !found {
				return true
			}
		}
		return false
	case "!&":
		// Check user is not in every group
		for _, r := range r.C {
			found := false
			for _, g := range groups {
				if r.Id == g.Id {
					found = true
				}
			}
			if found {
				return true
			}
		}
		return false
	case "|":
		// Check user is in one of the groups
		for _, r := range r.C {
			for _, g := range groups {
				if r.Id == g.Id {
					return false
				}
			}
		}
		return true
	case "!|":
		// Check user is not in one of the groups
		for _, r := range r.C {
			for _, g := range groups {
				if r.Id == g.Id {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

type Restriction struct {
	OP    string         `json:"op"`
	C     []RestrictionC `json:"c"`
	Show  bool           `json:"show"`
	ShowC []bool         `json:"showc"`
}

type RestrictionC struct {
	Type string `json:"type"`
	Id   int64  `json:"id"`
	D    string `json:"d"`
	T    int64  `json:"t"`
}

type CourseModule struct {
	Id           int64       `json:"id"`
	CourseId     int64       `json:"course"`
	ModuleId     int64       `json:"module"`
	InstanceId   int64       `json:"instance"`
	SectionId    int64       `json:"section"`
	ModuleName   string      `json:"modname"`
	Availability Restriction `json:"availability"`
	Name         string      `json:"name"`
	Grade        int64       `json:"grade"`
	Visible      bool        `json:"visible"`
	Added        *time.Time  `json:"added"`
}

func (m *MoodleApi) GetCourseModule(cmid int64) (*CourseModule, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&cmid=%d", m.base, m.token, "core_course_get_course_module", cmid)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type CourseModuleInt struct {
		Id           int64  `json:"id"`
		CourseId     int64  `json:"course"`
		ModuleId     int64  `json:"module"`
		InstanceId   int64  `json:"instance"`
		SectionId    int64  `json:"section"`
		ModuleName   string `json:"modname"`
		Name         string `json:"name"`
		Grade        int64  `json:"grade"`
		GradePass    string `json:"gradepass"`
		Availability string `json:"availability"`
		Added        int64  `json:"added"`
		Visible      int64  `json:"visible"`
	}

	type Result struct {
		CM CourseModuleInt `json:"cm"`
	}

	var result Result

	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	var t *time.Time
	if result.CM.Added != 0 {
		tt := time.Unix(result.CM.Added, 0)
		t = &tt
	}
	cm := &CourseModule{
		Id:         result.CM.Id,
		CourseId:   result.CM.CourseId,
		ModuleId:   result.CM.ModuleId,
		InstanceId: result.CM.InstanceId,
		SectionId:  result.CM.SectionId,
		ModuleName: result.CM.ModuleName,
		Name:       result.CM.Name,
		Grade:      result.CM.Grade,
		Visible:    result.CM.Visible == 1,
		Added:      t}

	if result.CM.Availability != "" {
		if err := json.Unmarshal([]byte(result.CM.Availability), &cm.Availability); err != nil {
			return nil, errors.New("Server returned unexpected response. " + err.Error())
		}
	}

	return cm, nil
}

type AssignmentInfo struct {
	Id                       int64      `json:"id"`
	CmId                     int64      `json:"cmid"`
	CourseId                 int64      `json:"courseid"`
	CourseCode               string     `json:"coursecode"`
	CourseName               string     `json:"coursename"`
	Name                     string     `json:"name"`
	NoSubmissions            int64      `json:"nosubmissions"`
	SubmissionDrafts         int64      `json:"submissiondrafts"`
	SendNotifications        int64      `json:"sendnotifications"`
	SendLateNotifications    int64      `json:"sendlatenotifications"`
	SendStudentNotifications int64      `json:"sendstudentnotifications"`
	Grade                    int64      `json:"grade"`
	CompletionSubmit         int64      `json:"completionsubmit"`
	CutoffDate               int64      `json:"cutoffdate"`
	AllowSubmissionsFromDate *time.Time `json:"allowsubmissionsfromdate"`
	DueDate                  *time.Time `json:"duedate"`
	GradingDueDate           *time.Time `json:"gradingduedate"`
	ExtensionDate            *time.Time `json:"extensiondate"`
}

func (m *MoodleApi) GetAssignmentsWithCourseId(courseIds []int) ([]*AssignmentInfo, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&includenotenrolledcourses=1", m.base, m.token, "mod_assign_get_assignments")
	for i, c := range courseIds {
		url = fmt.Sprintf("%s&courseids%%5B%d%%5D=%d", url, i, c)
	}
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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

	return assignments[:], nil
}

type QuizInfo struct {
	Id             int64      `json:"id"`
	CmId           int64      `json:"cmid"`
	CourseId       int64      `json:"courseid"`
	CourseCode     string     `json:"coursecode"`
	CourseName     string     `json:"coursename"`
	Name           string     `json:"name"`
	TimeClose      *time.Time `json:"duedate"`
	GradingDueDate *time.Time `json:"gradingduedate"`
	ExtensionDate  *time.Time `json:"extensiondate"`
}

func (m *MoodleApi) GetQuizzesWithCourseId(courseIds []int) ([]*QuizInfo, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "mod_quiz_get_quizzes_by_courses")
	for i, c := range courseIds {
		url = fmt.Sprintf("%s&courseids%%5B%d%%5D=%d", url, i, c)
	}
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type QuizResult struct {
		Id        int64  `json:"id"`
		CourseId  int64  `json:"course"`
		CmId      int64  `json:"coursemodule"`
		Name      string `json:"name"`
		TimeOpen  int64  `json:"timeopen"`
		TimeClose int64  `json:"timeclose"`
	}

	type Result struct {
		Quizzes []QuizResult `json:"quizzes"`
	}

	var results Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	assignments := make([]*QuizInfo, 0)
	for _, quiz := range results.Quizzes {
		var t *time.Time
		if quiz.TimeClose != 0 {
			tt := time.Unix(quiz.TimeClose, 0)
			t = &tt
		}
		ai := &QuizInfo{Id: quiz.Id, CmId: quiz.CmId, Name: quiz.Name, CourseId: quiz.Id, TimeClose: t}
		assignments = append(assignments, ai)
	}

	return assignments[:], nil
}

type ForumInfo struct {
	Id               int64      `json:"id"`
	CmId             int64      `json:"cmid"`
	CourseId         int64      `json:"courseid"`
	Scale            int64      `json:"scale"`
	Grade            int64      `json:"grade"`
	GradeForumNotify int64      `json:"grade_forum_notify"`
	Name             string     `json:"forum_name"`
	NumDiscussions   int64      `json:"numdiscussions"`
	Type             string     `json:"type"`
	Assessed         bool       `json:"assessed"`
	DueDate          *time.Time `json:"duedate"`
	CutoffDate       *time.Time `json:"cutoffdate"`
}

func (m *MoodleApi) GetForumsWithCourseId(courseIds []int) ([]*ForumInfo, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "mod_forum_get_forums_by_courses")
	for i, c := range courseIds {
		url = fmt.Sprintf("%s&courseids%%5B%d%%5D=%d", url, i, c)
	}
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type ForumResult struct {
		Id               int64  `json:"id"`
		CourseId         int64  `json:"course"`
		CmId             int64  `json:"cmid"`
		Name             string `json:"name"`
		DueDate          int64  `json:"duedate"`
		CutoffDate       int64  `json:"cutoffdate"`
		GradeForum       int64  `json:"grade_forum"`
		GradeForumNotify int64  `json:"grade_forum_notify"`
		Assessed         int64  `json:"assessed"`
		Scale            int64  `json:"scale"`
		NumDiscussions   int64  `json:"numdiscussions"`
		Type             string `json:"type"`
	}

	var results []ForumResult

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	assignments := make([]*ForumInfo, 0)
	for _, forum := range results {
		var dueDate *time.Time
		if forum.DueDate != 0 {
			tt := time.Unix(forum.DueDate, 0)
			dueDate = &tt
		}
		var cutoffDate *time.Time
		if forum.CutoffDate != 0 {
			tt := time.Unix(forum.CutoffDate, 0)
			cutoffDate = &tt
		}
		ai := &ForumInfo{
			Id:             forum.Id,
			Scale:          forum.Scale,
			CmId:           forum.CmId,
			Name:           forum.Name,
			CourseId:       forum.CourseId,
			Grade:          forum.GradeForum,
			Assessed:       forum.Assessed != 0,
			Type:           forum.Type,
			NumDiscussions: forum.NumDiscussions,
			DueDate:        dueDate,
			CutoffDate:     cutoffDate,
		}
		assignments = append(assignments, ai)
	}

	return assignments[:], nil
}

type ForumDiscussionResponse struct {
	Discussions []*ForumDiscussion `json:"discussions"`
	//Warnings    []ForumDiscussion `json:"warnings"`
}

type ForumDiscussion struct {
	Id                     int64      `json:"id"`
	Name                   string     `json:"name"`
	UserId                 int64      `json:"userid"`
	GroupId                int64      `json:"groupid"`
	TimeModified           *time.Time `json:"timemodified"`
	UserModified           *time.Time `json:"usermodified"`
	TimeStart              *time.Time `json:"timestart"`
	TimeEnd                *time.Time `json:"timeend"`
	Discussion             int64      `json:"discussion"`
	Parent                 int64      `json:"parent"`
	Created                *time.Time `json:"created"`
	Modified               *time.Time `json:"modified"`
	Mailed                 int64      `json:"created"`
	Subject                string     `json:"subject"`
	Message                string     `json:"message"`
	MessageFormat          int64      `json:"messageformat"`
	MessageTrust           int64      `json:"messagetrust"`
	Attachment             bool       `json:"attachment"`
	TotalScore             int64      `json:"totalscore"`
	MailNow                int64      `json:"mailnow"`
	UserFullName           string     `json:"userfullname"`
	UserModifiedFullName   string     `json:"usermodifiedfullname"`
	UserPictureUrl         string     `json:"userpictureurl"`
	UserModifiedPictureUrl string     `json:"usermodifiedpictureurl"`
	NumReplies             int64      `json:"numreplies"`
	NumUnread              int64      `json:"numunread"`
	Pinned                 bool       `json:"pinned"`
	Locked                 bool       `json:"locked"`
	Starred                bool       `json:"starred"`
	CanReply               bool       `json:"canreply"`
	CanLock                bool       `json:"canlock"`
	CanFavourite           bool       `json:"canfavourite"`
}

func (u *ForumDiscussion) UnmarshalJSON(data []byte) error {
	type Alias ForumDiscussion
	aux := &struct {
		TimeModified int64 `json:"timemodified"`
		UserModified int64 `json:"usermodified"`
		TimeStart    int64 `json:"timestart"`
		TimeEnd      int64 `json:"timeend"`
		Created      int64 `json:"created"`
		Modified     int64 `json:"modified"`
		*Alias
	}{
		Alias: (*Alias)(u),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a1 := time.Unix(aux.TimeModified, 0)
	u.TimeModified = &a1

	a2 := time.Unix(aux.UserModified, 0)
	u.UserModified = &a2

	a3 := time.Unix(aux.TimeStart, 0)
	u.TimeStart = &a3

	a4 := time.Unix(aux.TimeEnd, 0)
	u.TimeEnd = &a4

	a5 := time.Unix(aux.Created, 0)
	u.Created = &a5

	a6 := time.Unix(aux.Modified, 0)
	u.Modified = &a6

	return nil
}

func (m *MoodleApi) GetForumsDiscussions(forumId int) ([]*ForumDiscussion, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&forumid=%d", m.base, m.token, "mod_forum_get_forum_discussions", forumId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	var results ForumDiscussionResponse
	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return results.Discussions[:], nil
}

type AssignmentRecord struct {
	AssignmentId int64         `json:"assignmentid"`
	Grades       []GradeRecord `json:"grades"`
}

type GradeRecord struct {
	Id            int64   `json:"id"`
	UserId        int64   `json:"userid"`
	AttemptNumber int64   `json:"attemptnumber"`
	TimeCreated   int64   `json:"timecreated"`
	TimeModified  int64   `json:"timemodified"`
	Grader        int64   `json:"grade"`
	Grade         float64 `json:"grade"`
}

func (m *MoodleApi) GetAssignmentGrades(ids ...int64) (*[]AssignmentRecord, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json", m.base, m.token, "mod_assign_get_grades")
	for i, c := range ids {
		url = fmt.Sprintf("%s&assignmentids%%5B%d%%5D=%d", url, i, c)
	}
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Result struct {
		Assignments []AssignmentRecord `json:"assignments"`
	}

	var results Result

	if err := json.Unmarshal([]byte(body), &results); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	return &results.Assignments, nil
}

type AssignmentSubmission struct {
	Id            int64      `json:"id"`
	SubmissionId  int64      `json:"submissionid"`
	UserId        int64      `json:"userid"`
	Status        string     `json:"status"`
	GradingStatus string     `json:"gradingstatus"`
	Extension     *time.Time `json:"extensiondate"`
	TimeCreated   *time.Time `json:"timecreated"`
	TimeModified  *time.Time `json:"timemodified"`
}

func (m *MoodleApi) GetAssignmentSubmissions(assignmentId int64) ([]*AssignmentSubmission, error) {
	url := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&assignmentids[0]=%d", m.base, m.token, "mod_assign_get_submissions", assignmentId)
	m.log.Debug("Fetch: %s", url)
	body, _, _, err := m.fetch.GetUrl(url)

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
		TimeCreated   int64    `json:"timecreated"`
		TimeModified  int64    `json:"timemodified"`
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

	assignments := make([]*AssignmentSubmission, 0)
	for _, k := range results.Assignments {
		for _, i := range k.Submissions {
			var timeCreated *time.Time
			var timeModified *time.Time
			if i.TimeCreated != 0 {
				tt := time.Unix(i.TimeCreated, 0)
				timeCreated = &tt
			}
			if i.TimeModified != 0 {
				tt := time.Unix(i.TimeModified, 0)
				timeModified = &tt
			}
			assignments = append(assignments, &AssignmentSubmission{
				Id:            k.Id,
				SubmissionId:  i.Id,
				UserId:        i.UserId,
				Status:        i.Status,
				GradingStatus: i.GradingStatus,
				TimeCreated:   timeCreated,
				TimeModified:  timeModified,
			})
			//fmt.Println(i)
		}
	}

	url2 := fmt.Sprintf("%swebservice/rest/server.php?wstoken=%s&wsfunction=%s&moodlewsrestformat=json&assignmentids[0]=%d", m.base, m.token, "mod_assign_get_user_flags", assignmentId)
	m.log.Debug("Fetch: %s", url2)
	body, _, _, err = m.fetch.GetUrl(url2)

	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(body, "{\"exception\":\"") {
		return nil, errors.New(body)
	}

	type Flag struct {
		Id        int64 `json:"id"`
		UserId    int64 `json:"userid"`
		Extension int64 `json:"extensionduedate"`
	}

	type AssignFlag struct {
		Id        int64  `json:"assignmentid"`
		UserFlags []Flag `json:"userflags"`
	}

	type Result2 struct {
		Assignments []AssignFlag `json:"assignments"`
	}

	var results2 Result2

	if err := json.Unmarshal([]byte(body), &results2); err != nil {
		return nil, errors.New("Server returned unexpected response. " + err.Error())
	}

	for _, k := range results2.Assignments {
		for _, k := range k.UserFlags {
			// for each extension found, add or append to assignment list
			if k.Extension == 0 {
				continue
			}
			var t *time.Time
			tt := time.Unix(k.Extension, 0)
			t = &tt
			found := false
			for _, a := range assignments {
				if a.UserId == k.UserId && k.Extension > 0 {
					a.Extension = t
				}
			}
			if !found {
				assignments = append(assignments, &AssignmentSubmission{UserId: k.UserId, Status: "new", GradingStatus: "", Extension: t})

			}
		}
	}

	return assignments[:], nil
}

func GetAttendance() error {

	// Get attendance for a session

	// But how to we know which sessions to look at?

	return nil
}

func (m *MoodleApi) SetUrlFetcher(fetch LookupUrl) {
	m.fetch = fetch
}
