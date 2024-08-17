package gmaps

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	"github.com/joho/godotenv"
	"github.com/mcnijman/go-emailaddress"
)

type EmailExtractJob struct {
	scrapemate.Job
	Entry *Entry
}

type CEIDGExtractJob struct {
	scrapemate.Job
	Entry *Entry
}

func NewEmailJob(parentID string, entry *Entry) *EmailExtractJob {
	return &EmailExtractJob{
		Job: scrapemate.Job{
			ParentID:   parentID,
			Method:     "GET",
			URL:        entry.WebSite,
			MaxRetries: 0,
			Priority:   scrapemate.PriorityHigh,
		},
		Entry: entry,
	}
}

func (j *EmailExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
	}()

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing email job", "url", j.URL)

	if resp.Error != nil {
		return j.Entry, nil, nil
	}

	doc, ok := resp.Document.(*goquery.Document)
	if !ok {
		return j.Entry, nil, nil
	}

	emails := docEmailExtractor(doc)
	if len(emails) == 0 {
		emails = regexEmailExtractor(resp.Body)
	}

	j.Entry.Emails = emails
	socialLinks := extractSocialLinks(doc)
	for key, value := range socialLinks {
		j.Entry.SocialLinks[key] = value
	}

	nip := extractNIP(resp.Body)
	j.Entry.NIP = nip

	if nip != "" {
		job := NewCEIDGJob(j.Entry)
		return j.Entry, []scrapemate.IJob{job}, nil
	}

	return j.Entry, nil, nil
}

func (j *EmailExtractJob) ProcessOnFetchError() bool {
	return true
}

func docEmailExtractor(doc *goquery.Document) []string {
	seen := map[string]bool{}
	var emails []string

	doc.Find("a[href^='mailto:']").Each(func(_ int, s *goquery.Selection) {
		mailto, exists := s.Attr("href")
		if exists {
			value := strings.TrimPrefix(mailto, "mailto:")
			if email, err := getValidEmail(value); err == nil {
				if !seen[email] {
					emails = append(emails, email)
					seen[email] = true
				}
			}
		}
	})

	return emails
}

func regexEmailExtractor(body []byte) []string {
	seen := map[string]bool{}
	var emails []string

	addresses := emailaddress.Find(body, false)
	for i := range addresses {
		if !seen[addresses[i].String()] {
			emails = append(emails, addresses[i].String())
			seen[addresses[i].String()] = true
		}
	}

	return emails
}

func getValidEmail(s string) (string, error) {
	email, err := emailaddress.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}

	return email.String(), nil
}

func extractSocialLinks(doc *goquery.Document) map[string]string {
	socialLinks := make(map[string]string)

	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			if strings.Contains(href, "facebook") {
				socialLinks["facebook"] = href
			}
			if strings.Contains(href, "instagram") {
				socialLinks["instagram"] = href
			}
			if strings.Contains(href, "twitter") {
				socialLinks["twitter"] = href
			}
		}
	})

	return socialLinks
}

func extractNIP(body []byte) string {
	re := regexp.MustCompile(`((\d{3}[- ]\d{3}[- ]\d{2}[- ]\d{2})|(\d{3}[- ]\d{2}[- ]\d{2}[- ]\d{3}))`)
	matches := re.Find(body)
	if matches != nil {
		return cleanNIP(string(matches))
	}
	return ""
}

func cleanNIP(nip string) string {
	return strings.ReplaceAll(strings.ReplaceAll(nip, "-", ""), " ", "")
}

func NewCEIDGJob(entry *Entry) *CEIDGExtractJob {
	url := fmt.Sprintf("https://api.firmateka.pl/ceidg/firmy?nip=%s", cleanNIP(entry.NIP))

	return &CEIDGExtractJob{
		Job: scrapemate.Job{
			Method:     "GET",
			URL:        url,
			MaxRetries: 0,
			Priority:   scrapemate.PriorityHigh,
		},
		Entry: entry,
	}
}

func (j *CEIDGExtractJob) Process(ctx context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	log := scrapemate.GetLoggerFromContext(ctx)
	log.Info("Processing CEIDG job", "url", j.URL)

	// Załaduj zmienne środowiskowe
	err := godotenv.Load()
	if err != nil {
		log.Error("Error loading .env file")
		return j.Entry, nil, fmt.Errorf("could not load .env file")
	}

	// Pobierz klucz API
	apiKey := os.Getenv("FIRMATEKA_API_KEY")
	if apiKey == "" {
		log.Error("API key not found in environment variables")
		return j.Entry, nil, fmt.Errorf("missing API key")
	}

	// Dodaj debugowanie, aby sprawdzić, czy klucz API został poprawnie załadowany
	log.Info("Loaded API key", "apiKey", apiKey)

	// Utworzenie zapytania HTTP
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", j.URL, nil)
	if err != nil {
		log.Error("Error creating request", "error", err)
		return j.Entry, nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	respAPI, err := client.Do(req)
	if err != nil {
		log.Error("Error sending request to Firmateka API", "error", err)
		return j.Entry, nil, err
	}
	defer respAPI.Body.Close()

	log.Info("Response Status Code", "statusCode", respAPI.StatusCode)

	body, err := ioutil.ReadAll(respAPI.Body)
	if err != nil {
		log.Error("Error reading response body", "error", err)
		return j.Entry, nil, err
	}
	log.Info("Response Body", "body", string(body))

	// Przetwarzanie odpowiedzi jako JSON
	var firmatekaResponse struct {
		Firmy []struct {
			ID                string `json:"id"`
			Nazwa             string `json:"nazwa"`
			AdresDzialalnosci struct {
				Ulica       string `json:"ulica"`
				Budynek     string `json:"budynek"`
				Miasto      string `json:"miasto"`
				Wojewodztwo string `json:"wojewodztwo"`
				Powiat      string `json:"powiat"`
				Gmina       string `json:"gmina"`
				Kraj        string `json:"kraj"`
				Kod         string `json:"kod"`
			} `json:"adresDzialalnosci"`
			Wlasciciel struct {
				Imie     string `json:"imie"`
				Nazwisko string `json:"nazwisko"`
				Nip      string `json:"nip"`
				Regon    string `json:"regon"`
			} `json:"wlasciciel"`
			DataRozpoczecia string `json:"dataRozpoczecia"`
			Status          string `json:"status"`
			Link            string `json:"link"`
		} `json:"firmy"`
	}

	err = json.Unmarshal(body, &firmatekaResponse)
	if err != nil {
		log.Error("Error unmarshalling Firmateka response", "error", err)
		return j.Entry, nil, err
	}

	// Sprawdzamy, czy odpowiedź zawiera dane firmy
	if len(firmatekaResponse.Firmy) > 0 {
		firma := firmatekaResponse.Firmy[0]

		ceidgData := fmt.Sprintf(`{
			"id": "%s",
			"nazwa": "%s",
			"wlasciciel": {
				"imie": "%s",
				"nazwisko": "%s",
				"nip": "%s",
				"regon": "%s"
			},
			"adresDzialalnosci": {
				"ulica": "%s",
				"budynek": "%s",
				"miasto": "%s",
				"wojewodztwo": "%s",
				"powiat": "%s",
				"gmina": "%s",
				"kraj": "%s",
				"kod": "%s"
			},
			"dataRozpoczecia": "%s",
			"status": "%s",
			"link": "%s"
		}`,
			firma.ID,
			firma.Nazwa,
			firma.Wlasciciel.Imie, firma.Wlasciciel.Nazwisko, firma.Wlasciciel.Nip, firma.Wlasciciel.Regon,
			firma.AdresDzialalnosci.Ulica, firma.AdresDzialalnosci.Budynek, firma.AdresDzialalnosci.Miasto,
			firma.AdresDzialalnosci.Wojewodztwo, firma.AdresDzialalnosci.Powiat, firma.AdresDzialalnosci.Gmina,
			firma.AdresDzialalnosci.Kraj, firma.AdresDzialalnosci.Kod,
			firma.DataRozpoczecia, firma.Status, firma.Link)

		j.Entry.CEIDG = ceidgData
	} else {
		log.Info("Firmateka data not found", "url", j.URL)
	}

	return j.Entry, nil, nil
}
