package gmaps

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Entry struct {
	ID            string   `json:"input_id"`
	Link          string   `json:"link"`
	Title         string   `json:"title"`
	Address       Address  `json:"complete_address"`
	City          string   `json:"city"`
	WebSite       string   `json:"web_site"`
	Phone         string   `json:"phone"`
	Emails        []string `json:"emails"`
	SocialLinks   map[string]string `json:"social_links"` // Added JSON tag
	NIP           string   `json:"nip"`
	CEIDG         string   `json:"ceidg"`
}

type Address struct {
	Street string `json:"street"`
	Number string `json:"number"`
}

func (e *Entry) CsvHeaders() []string {
	return []string{
		"title",
		"address",
		"city",
		"website",
		"phone",
		"emails",
		"facebook",
		"instagram",
		"twitter",
		"nip",
		"ceidg",
	}
}

func (e *Entry) CsvRow() []string {
	address := fmt.Sprintf("%s %s", e.Address.Street, e.Address.Number)

	return []string{
		e.Title,
		address,
		e.City,
		e.WebSite,
		e.Phone,
		stringSliceToString(e.Emails),
		e.SocialLinks["facebook"],
		e.SocialLinks["instagram"],
		e.SocialLinks["twitter"],
		e.NIP,
		e.CEIDG,
	}
}

func (e *Entry) IsWebsiteValidForEmail() bool {
	if e.WebSite == "" {
		return false
	}

	needles := []string{
		"facebook",
		"instagram",
		"twitter",
	}

	for _, needle := range needles {
		if strings.Contains(e.WebSite, needle) {
			return false
		}
	}

	return true
}

func EntryFromJSON(raw []byte) (Entry, error) {
	var jd []any
	if err := json.Unmarshal(raw, &jd); err != nil {
		return Entry{}, err
	}

	if len(jd) < 7 {
		return Entry{}, fmt.Errorf("invalid json")
	}

	darray, ok := jd[6].([]any)
	if !ok {
		return Entry{}, fmt.Errorf("invalid json")
	}

	entry := Entry{
		ID:     getNthElementAndCast[string](darray, 0),
		Link:   getNthElementAndCast[string](darray, 1),
		Title:  getNthElementAndCast[string](darray, 11),
		City:   getNthElementAndCast[string](darray, 183, 1, 3),
		WebSite: getNthElementAndCast[string](darray, 7, 0),
		Phone:  getNthElementAndCast[string](darray, 178, 0, 0),
		Emails: getNthElementAndCast[[]string](darray, 5),
	}

	// Extract the complete address
	fullAddress := getNthElementAndCast[string](darray, 18) // Assuming complete address is at index 18
	street, number := extractStreetAndNumber(fullAddress)
	entry.Address = Address{Street: street, Number: number}

	// Initialize social links map
	entry.SocialLinks = make(map[string]string)

	// Extract social media links
	if strings.Contains(entry.WebSite, "facebook") {
		entry.SocialLinks["facebook"] = entry.WebSite
	}
	if strings.Contains(entry.WebSite, "instagram") {
		entry.SocialLinks["instagram"] = entry.WebSite
	}
	if strings.Contains(entry.WebSite, "twitter") {
		entry.SocialLinks["twitter"] = entry.WebSite
	}

	// Additional logic to handle other sources of social media links if available
	// e.g., from other fields or sources

	return entry, nil
}

func extractStreetAndNumber(fullAddress string) (string, string) {
	// Use regex to separate company name and other components from the address
	re := regexp.MustCompile(`^(.*?),\s*(.*?)(?:, \d{2}-\d{3})?(?:, .+)?$`)
	matches := re.FindStringSubmatch(fullAddress)

	if len(matches) < 3 {
		return "", ""
	}

	address := matches[2]
	addressParts := strings.SplitN(address, " ", 2)
	street := ""
	number := ""
	if len(addressParts) > 1 {
		street = addressParts[0]
		number = addressParts[1]
	} else {
		street = addressParts[0]
	}

	return street, number
}

func getNthElementAndCast[T any](arr []any, indexes ...int) T {
	var (
		defaultVal T
		idx        int
	)

	if len(indexes) == 0 {
		return defaultVal
	}

	for len(indexes) > 1 {
		idx, indexes = indexes[0], indexes[1:]

		if idx >= len(arr) {
			return defaultVal
		}

		next := arr[idx]

		if next == nil {
			return defaultVal
		}

		var ok bool

		arr, ok = next.([]any)
		if !ok {
			return defaultVal
		}
	}

	if len(indexes) == 0 || len(arr) == 0 {
		return defaultVal
	}

	ans, ok := arr[indexes[0]].(T)
	if !ok {
		return defaultVal
	}

	return ans
}

func stringSliceToString(s []string) string {
	return strings.Join(s, ", ")
}