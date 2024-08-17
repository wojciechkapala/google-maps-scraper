package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/playwright-community/playwright-go"
	"github.com/wojciechkapala/google-maps-scraper/gmaps"
)

var args arguments

func main() {

	// just install playwright
	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		if err := installPlaywright(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Parsowanie flag odbywa się tylko raz
	args = parseArgs()

	// Użycie sync.WaitGroup, aby program nie zakończył się przedwcześnie
	var wg sync.WaitGroup
	wg.Add(1)

	// Uruchomienie serwera HTTP w gorutynie
	go func() {
		defer wg.Done()
		startServer()
	}()

	// Informacja, że serwer został uruchomiony
	fmt.Println("Serwer został uruchomiony i nasłuchuje na porcie 8015")

	// Czekanie na zakończenie gorutyny
	wg.Wait()
}

type scrapeRequest struct {
	LangCode    string `json:"langCode"`
	MaxDepth    int    `json:"maxDepth"`
	Email       bool   `json:"email"`
	Phrase      string `json:"phrase"` // Nowe pole - opcjonalna fraza
	ResultsFile string `json:"resultsFile"`
	InputFile   string `json:"inputFile"`
	Json        bool   `json:"json"`
}

type scrapeResponse struct {
	Message   string `json:"message"`
	Records   int    `json:"records"`
	InputFile string `json:"inputFile"`
}

func startScraperInBackground(args arguments) {
	go func() {
		ctx := context.Background()
		fmt.Println("Scraper rozpoczął pracę w tle...")
		err := runScraper(ctx, args)
		if err != nil {
			fmt.Printf("Błąd scraper'a: %v\n", err)
		}
		fmt.Println("Scraper zakończył pracę.")
	}()
}

type replaceRequest struct {
	Phrase string `json:"phrase"` // Fraza, która zastąpi słowo "fraza" w szablonie
}

func startServer() {
	router := gin.Default()

	// Endpoint do sprawdzenia statusu
	router.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "API działa poprawnie"})
	})

	router.POST("/scrape", func(c *gin.Context) {
		var req scrapeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Nieprawidłowe dane wejściowe"})
			return
		}

		// Jeśli phrase jest podane, tworzymy plik input i ustawiamy nazwę pliku wynikowego
		if req.Phrase != "" {
			// Tworzymy nowy plik tekstowy z miastami na podstawie szablonu
			templateContent, err := ioutil.ReadFile("miasta.txt")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas odczytywania szablonu"})
				return
			}

			// Zastępujemy "fraza" na dostarczoną frazę
			newContent := strings.ReplaceAll(string(templateContent), "fraza", req.Phrase)

			// Tworzymy nowy plik wynikowy i wejściowy
			inputFileName := fmt.Sprintf("%s_miasta.txt", req.Phrase)
			err = ioutil.WriteFile(inputFileName, []byte(newContent), 0644)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas zapisywania nowego pliku wejściowego"})
				return
			}

			// Ustawiamy inputFile i resultsFile na podstawie phrase
			req.InputFile = inputFileName
			req.ResultsFile = fmt.Sprintf("%s_results.csv", req.Phrase)
		}

		// Sprawdzenie, czy pliki zostały ustawione, jeśli nie ustawiamy na domyślne wartości
		if req.InputFile == "" {
			req.InputFile = "default_input.txt" // Można dostosować
		}
		if req.ResultsFile == "" {
			req.ResultsFile = "default_results.csv" // Można dostosować
		}

		// Konfigurujemy argumenty dla scraper'a
		args := arguments{
			langCode:    req.LangCode,
			maxDepth:    req.MaxDepth,
			email:       req.Email,
			resultsFile: req.ResultsFile,
			inputFile:   req.InputFile,
			json:        req.Json,
			concurrency: runtime.NumCPU() / 2,
		}

		ctx := context.Background()
		err := runScraper(ctx, args)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":     "Scraper został uruchomiony",
			"inputFile":   req.InputFile,
			"resultsFile": req.ResultsFile,
		})
	})

	// Nowy endpoint do tworzenia pliku tekstowego na podstawie szablonu
	router.POST("/createfile", func(c *gin.Context) {
		// Oczekujemy JSON z frazą do zamiany
		var req replaceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Nieprawidłowe dane wejściowe"})
			return
		}

		// Odczytujemy szablon z pliku miasta.txt
		templateContent, err := ioutil.ReadFile("miasta.txt")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas odczytywania szablonu"})
			return
		}

		// Zastępujemy "fraza" na dostarczoną frazę
		newContent := strings.ReplaceAll(string(templateContent), "fraza", req.Phrase)

		// Tworzymy nowy plik wynikowy
		outputFileName := fmt.Sprintf("%s_miasta.txt", req.Phrase)
		err = ioutil.WriteFile(outputFileName, []byte(newContent), 0644)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Błąd podczas zapisywania nowego pliku"})
			return
		}

		// Zwracamy informację o utworzonym pliku
		c.JSON(http.StatusOK, gin.H{"message": "Plik został utworzony", "file": outputFileName})
	})

	// Serwer będzie nasłuchiwał na porcie 8080
	router.Run(":8015")
}

func runScraper(ctx context.Context, args arguments) error {
	fmt.Println("Uruchamianie scraper'a...") // Debugowanie

	// Zakładam, że nie korzystasz z bazy danych, więc pomiń `runFromDatabase`
	if args.dsn == "" {
		return runFromLocalFile(ctx, &args)
	}

	// Jeśli potrzebujesz obsługi bazy danych, musisz zaimplementować `runFromDatabase`
	return fmt.Errorf("Obsługa bazy danych nie jest zaimplementowana")
}

func createSeedJobs(langCode string, r io.Reader, maxDepth int, email bool) ([]scrapemate.IJob, error) {
	fmt.Println("Rozpoczynam tworzenie zadań...") // Debugowanie

	jobs := []scrapemate.IJob{}
	scanner := bufio.NewScanner(r)

	lineNumber := 0 // Licznik linii do debugowania
	for scanner.Scan() {
		lineNumber++
		query := strings.TrimSpace(scanner.Text())
		fmt.Printf("Przetwarzam linię %d: %s\n", lineNumber, query) // Debugowanie

		// Jeśli linia jest pusta, pomiń
		if query == "" {
			fmt.Printf("Pusta linia, pomijam linię %d\n", lineNumber) // Debugowanie
			continue
		}

		var id string
		if before, after, ok := strings.Cut(query, "#!#"); ok {
			query = strings.TrimSpace(before)
			id = strings.TrimSpace(after)
			fmt.Printf("Zidentyfikowano ID: %s dla zapytania: %s\n", id, query) // Debugowanie
		}

		// Tworzenie nowego zadania GmapJob
		fmt.Println("Tworzę nowe zadanie GmapJob...") // Debugowanie
		job := gmaps.NewGmapJob(id, langCode, query, maxDepth, email)
		jobs = append(jobs, job)
		fmt.Printf("Dodano zadanie: %v\n", job) // Debugowanie
	}

	// Obsługa błędów skanowania
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("Błąd podczas skanowania pliku: %v", err)
	}

	fmt.Printf("Utworzono %d zadań\n", len(jobs)) // Debugowanie
	return jobs, nil
}

func runFromLocalFile(ctx context.Context, args *arguments) error {
	fmt.Println("Rozpoczynam przetwarzanie lokalnego pliku...") // Debugowanie

	// Otwieranie pliku wejściowego lub czytanie z stdin
	var input io.Reader
	switch args.inputFile {
	case "stdin":
		fmt.Println("Czytam dane ze stdin") // Debugowanie
		input = os.Stdin
	default:
		fmt.Println("Otwieram plik:", args.inputFile) // Debugowanie
		f, err := os.Open(args.inputFile)
		if err != nil {
			return fmt.Errorf("Błąd podczas otwierania pliku %s: %v", args.inputFile, err)
		}
		defer func() {
			fmt.Println("Zamykam plik wejściowy") // Debugowanie
			f.Close()
		}()
		input = f
	}

	// Otwieranie pliku wynikowego lub pisanie na stdout
	var resultsWriter io.Writer
	switch args.resultsFile {
	case "stdout":
		fmt.Println("Zapisuję wyniki na stdout") // Debugowanie
		resultsWriter = os.Stdout
	default:
		fmt.Println("Tworzę plik wynikowy:", args.resultsFile) // Debugowanie
		f, err := os.Create(args.resultsFile)
		if err != nil {
			return fmt.Errorf("Błąd podczas tworzenia pliku wynikowego %s: %v", args.resultsFile, err)
		}
		defer func() {
			fmt.Println("Zamykam plik wynikowy") // Debugowanie
			f.Close()
		}()
		resultsWriter = f
	}

	// Ustawienie formatu zapisu wyników
	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(resultsWriter))
	writers := []scrapemate.ResultWriter{}
	if args.json {
		fmt.Println("Zapisuję wyniki w formacie JSON") // Debugowanie
		writers = append(writers, jsonwriter.NewJSONWriter(resultsWriter))
	} else {
		fmt.Println("Zapisuję wyniki w formacie CSV") // Debugowanie
		writers = append(writers, csvWriter)
	}

	// Opcje konfiguracji aplikacji
	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(args.concurrency),
		scrapemateapp.WithExitOnInactivity(args.exitOnInactivityDuration),
	}

	// Obsługa trybu debugowania
	if args.debug {
		fmt.Println("Tryb debugowania jest włączony: Uruchamiam w trybie headfull i wyłączam obrazy") // Debugowanie
		opts = append(opts, scrapemateapp.WithJS(
			scrapemateapp.Headfull(),
			scrapemateapp.DisableImages(),
		))
	} else {
		fmt.Println("Uruchamiam w trybie headless") // Debugowanie
		opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
	}

	// Tworzenie nowej konfiguracji aplikacji
	fmt.Println("Tworzę nową konfigurację aplikacji...") // Debugowanie
	cfg, err := scrapemateapp.NewConfig(writers, opts...)
	if err != nil {
		return fmt.Errorf("Błąd podczas tworzenia konfiguracji aplikacji: %v", err)
	}

	// Tworzenie nowej instancji aplikacji
	fmt.Println("Tworzę nową instancję aplikacji ScrapeMate...") // Debugowanie
	app, err := scrapemateapp.NewScrapeMateApp(cfg)
	if err != nil {
		return fmt.Errorf("Błąd podczas tworzenia aplikacji ScrapeMate: %v", err)
	}

	// Tworzenie zadań (jobs) na podstawie wejścia
	fmt.Println("Tworzenie zadań...") // Debugowanie
	seedJobs, err := createSeedJobs(args.langCode, input, args.maxDepth, args.email)
	if err != nil {
		return fmt.Errorf("Błąd podczas tworzenia zadań: %v", err)
	}

	// Uruchamianie aplikacji ScrapeMate
	fmt.Println("Rozpoczynanie działania aplikacji ScrapeMate...") // Debugowanie
	err = app.Start(ctx, seedJobs...)
	if err != nil {
		return fmt.Errorf("Błąd podczas uruchamiania ScrapeMate: %v", err)
	}

	fmt.Println("Zakończono działanie aplikacji ScrapeMate.") // Debugowanie
	return nil
}

func installPlaywright() error {
	return playwright.Install()
}

type arguments struct {
	concurrency              int
	cacheDir                 string
	maxDepth                 int
	inputFile                string
	resultsFile              string
	json                     bool
	langCode                 string
	debug                    bool
	dsn                      string
	produceOnly              bool
	exitOnInactivityDuration time.Duration
	email                    bool
}

func parseArgs() (args arguments) {
	const (
		defaultDepth      = 10
		defaultCPUDivider = 2
	)

	defaultConcurency := runtime.NumCPU() / defaultCPUDivider
	if defaultConcurency < 1 {
		defaultConcurency = 1
	}

	flag.IntVar(&args.concurrency, "c", defaultConcurency, "sets the concurrency. By default it is set to half of the number of CPUs")
	flag.StringVar(&args.cacheDir, "cache", "cache", "sets the cache directory (no effect at the moment)")
	flag.IntVar(&args.maxDepth, "depth", defaultDepth, "is how much you allow the scraper to scroll in the search results. Experiment with that value")
	flag.StringVar(&args.resultsFile, "results", "stdout", "is the path to the file where the results will be written")
	flag.StringVar(&args.inputFile, "input", "stdin", "is the path to the file where the queries are stored (one query per line). By default it reads from stdin")
	flag.StringVar(&args.langCode, "lang", "en", "is the languate code to use for google (the hl urlparam).Default is en . For example use de for German or el for Greek")
	flag.BoolVar(&args.debug, "debug", false, "Use this to perform a headfull crawl (it will open a browser window) [only when using without docker]")
	flag.StringVar(&args.dsn, "dsn", "", "Use this if you want to use a database provider")
	flag.BoolVar(&args.produceOnly, "produce", false, "produce seed jobs only (only valid with dsn)")
	flag.DurationVar(&args.exitOnInactivityDuration, "exit-on-inactivity", 0, "program exits after this duration of inactivity(example value '5m')")
	flag.BoolVar(&args.json, "json", false, "Use this to produce a json file instead of csv (not available when using db)")
	flag.BoolVar(&args.email, "email", false, "Use this to extract emails from the websites")

	flag.Parse()

	return args
}

func openPsqlConn(dsn string) (conn *sql.DB, err error) {
	conn, err = sql.Open("pgx", dsn)
	if err != nil {
		return
	}

	err = conn.Ping()

	return
}
