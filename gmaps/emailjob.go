package gmaps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
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
	url := fmt.Sprintf("https://wl-api.mf.gov.pl/api/search/nip/%s?date=%s", cleanNIP(entry.NIP), time.Now().Format("2006-01-02"))

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

	if resp.Error != nil {
		return j.Entry, nil, nil
	}

	body, err := ioutil.ReadAll(bytes.NewReader(resp.Body))
	if err != nil {
		log.Error("Error reading response body from CEIDG API", "error", err)
		return j.Entry, nil, err
	}

	var ceidgResponse map[string]interface{}
	err = json.Unmarshal(body, &ceidgResponse)
	if err != nil {
		log.Error("Error unmarshalling CEIDG response", "error", err)
		return j.Entry, nil, err
	}

	if result, ok := ceidgResponse["result"].(map[string]interface{}); ok {
		name, _ := result["name"].(string)
		nip, _ := result["nip"].(string)
		statusVat, _ := result["statusVat"].(string)
		regon, _ := result["regon"].(string)
		residenceAddress, _ := result["residenceAddress"].(string)
		legalDate, _ := result["registrationLegalDate"].(string)

		ceidgData := fmt.Sprintf(`{"name":"%s","nip":"%s","statusVat":"%s","regon":"%s","residenceAddress":"%s","registrationLegalDate":"%s"}`,
			name, nip, statusVat, regon, residenceAddress, legalDate)
		j.Entry.CEIDG = ceidgData
	} else {
		log.Info("CEIDG data not found", "url", j.URL)
	}

	return j.Entry, nil, nil
}