package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
)

// initial information sent by duck.ai
type ChatInformation struct {
	Role    string `json:"role"`
	Message string `json:"message"`
	Created int64  `json:"created"`
	Id      string `json:"id"`
	Action  string `json:"action"`
	Model   string `json:"model"`
}

// actual message fragments from duck.ai
type MessageFragment struct {
	Message string `json:"message"`
	Created int64  `json:"created"`
	Id      string `json:"id"`
	Action  string `json:"action"`
	Model   string `json:"model"`
}

type Args struct {
	Prompt      string
	Model       string
	Interactive bool
}

var logger = log.New(os.Stderr, "[qduck] ", log.Lshortfile|log.LUTC|log.Ltime|log.Lmicroseconds|log.Ldate)

func getVqdToken() (string, error) {
	req, err := http.NewRequest("GET", "https://duckduckgo.com/duckchat/v1/status", nil)

	if err != nil {
		return "", fmt.Errorf("failed to make a new HTTP request: %s", err)
	}

	req.Header.Set("X-Vqd-Accept", "1")
	req.Header.Set("Referer", "https://duckduckgo.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.6778.86 Safari/537.36")

	client := &http.Client{}

	logger.Println("Getting VQD token from duck.ai")
	resp, err := client.Do(req)

	if err != nil {
		return "", fmt.Errorf("failed to act on request: %s", err)
	}

	defer resp.Body.Close()

	vqd := resp.Header.Get("x-vqd-4")

	return vqd, nil
}

func prompt(input, model string) (string, error) {
	vqd, err := getVqdToken()
	if err != nil {
		return "", fmt.Errorf("HANDLE ERROR: %s", err)
	}

	bodystring := `{"model": "%s", "messages": [{"role": "user","content": "%s"}]}`
	formattedstring := fmt.Sprintf(bodystring, model, input)
	jsondata := []byte(formattedstring)

	req, err := http.NewRequest("POST", "https://duckduckgo.com/duckchat/v1/chat", bytes.NewBuffer(jsondata))
	if err != nil {
		return "", fmt.Errorf("HANDLE ERROR: %s", err)
	}

	req.Header.Set("X-Vqd-4", vqd)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Referer", "https://duckduckgo.com/")
	req.Header.Set("Origin", "https://duckduckgo.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.6778.86 Safari/537.36")
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{}

	logger.Println("Sending prompt to duck.ai")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HANDLE ERROR: %s", err)
	}
	defer resp.Body.Close()

	var responseBuilder strings.Builder
	scanner := bufio.NewScanner(resp.Body)

	lineNumber := 0
	var line string
	for scanner.Scan() {
		// first line seems to look like chat information
		lineNumber++

		if lineNumber == 1 {
			info := scanner.Text()

			var chatInfo ChatInformation
			if err := json.Unmarshal([]byte(stripJsonEventStreamPrefix(info)), &chatInfo); err != nil {
				logger.Printf("Failed to unmarshal chat information: %v\n", err)
			}
			// convert localtime unix timestamp to UTC RFC3339 time
			var parsedUnixTime = time.Unix(chatInfo.Created, 0).UTC().Format(time.RFC3339)
			var logMessage = `Chat information: using model %s, created at %s, with id %s, as %s`
			logger.Printf(logMessage, chatInfo.Model, parsedUnixTime, chatInfo.Id, chatInfo.Role)
		}

		line = scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := line[6:]

		// everything after this prefix is message fragments and valid json
		var frag MessageFragment
		if err := json.Unmarshal([]byte(jsonData), &frag); err != nil {
			continue
		}

		// add the message fragment to the final response from ai
		responseBuilder.WriteString(frag.Message)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("Error reading stream: %v", err)
	}

	return responseBuilder.String(), nil
}

func stripJsonEventStreamPrefix(input string) string {
	return strings.TrimPrefix(input, "data: ")
}

func main() {
	// Key-value list of abbreviated names and API names for models
	models := map[string]string{
		"gpt-4o-mini": "gpt-4o-mini",
		"o3-mini":     "o3-mini",
		"llama-3.3":   "meta-llama/Llama-3.3-70B-Instruct-Turbo",
		"claude-3":    "claude-3-haiku-20240307",
		"mixtral":     "mistralai/Mixtral-8x7B-Instruct-v0.1",
	}

	var args Args
	flag.StringVar(&args.Model, "model", "gpt-4o-mini", "Model to use for prompt. Available models are: gpt-4o-mini, o3-mini, llama-3.3, claude-3, mixtral")
	flag.StringVar(&args.Prompt, "prompt", "", "Prompt to send to model")
	flag.BoolVar(&args.Interactive, "int", false, "Enable interative mode")
	flag.Parse()

	if args.Interactive {
		handleInteractiveMode(&args, models)
	} else {
		err := handleCliMode(&args)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	// Check if the selected model is valid
	if _, ok := models[args.Model]; !ok {
		fmt.Println("Invalid model selected.")
		return
	}

	fmt.Printf("Sending response to model %s\n", args.Model)
	response, err := prompt(args.Prompt, models[args.Model])

	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(response)
}

func handleInteractiveMode(args *Args, models map[string]string) {
	var modelOptions []string
	for key := range models {
		modelOptions = append(modelOptions, key)
	}

	modelPrompt := &survey.Select{
		Message: "Choose a model:",
		Options: modelOptions,
	}
	survey.AskOne(modelPrompt, &args.Model)

	promptPrompt := &survey.Input{
		Message: "Enter your prompt:",
	}

	survey.AskOne(promptPrompt, &args.Prompt)
}

func handleCliMode(args *Args) error {
	if args.Prompt == "" {
		if len(flag.Args()) > 0 {
			args.Prompt = flag.Args()[0]
		} else {
			return fmt.Errorf("Please provide a prompt either via the -prompt flag or as the first positional argument.")
		}
	}

	return nil
}
