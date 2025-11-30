package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/anishathalye/porcupine"
	"github.com/cs598rap/raft-kubernetes/server/pkg/kvstore"
)

func main() {
	// Command line flags
	historyFile := flag.String("file", "", "Path to history JSON file")
	apiURL := flag.String("url", "", "URL to fetch history from API (e.g., http://localhost:8080/history)")
	outputFile := flag.String("output", "", "Path to save visualization output")
	verbose := flag.Bool("verbose", false, "Verbose output")
	flag.Parse()

	var history []kvstore.Operation
	var err error

	// Load history from file or API
	if *historyFile != "" {
		history, err = loadHistoryFromFile(*historyFile)
		if err != nil {
			log.Fatalf("Failed to load history from file: %v", err)
		}
	} else if *apiURL != "" {
		history, err = fetchHistoryFromAPI(*apiURL)
		if err != nil {
			log.Fatalf("Failed to fetch history from API: %v", err)
		}
	} else {
		log.Fatal("Must specify either --file or --url")
	}

	if len(history) == 0 {
		fmt.Println("No operations in history")
		return
	}

	fmt.Printf("Loaded %d operations from history\n", len(history))

	// Convert to Porcupine events
	events := convertToPorcupineEvents(history)

	if *verbose {
		fmt.Println("\nOperations:")
		for i, op := range history {
			fmt.Printf("%d: %s %s %s (client=%s, start=%v, end=%v)\n",
				i, op.Type, op.Key, op.Value, op.ClientID,
				op.StartTime.Format(time.RFC3339Nano),
				op.EndTime.Format(time.RFC3339Nano))
		}
		fmt.Println()
	}

	// Check linearizability (verbose to get visualization info)
	fmt.Println("Checking linearizability...")
	result, info := porcupine.CheckEventsVerbose(KVModel, events, 0)

	if result == porcupine.Ok {
		fmt.Println("✓ PASS: History is linearizable")
		os.Exit(0)
	} else if result == porcupine.Illegal {
		fmt.Println("✗ FAIL: History is NOT linearizable")

		// Try to find a specific violation
		if len(events) > 0 {
			fmt.Println("\nSearching for violation...")
			fmt.Printf("\nViolation details:\n")
			fmt.Printf("  Type: Linearizability violation detected\n")
			fmt.Printf("  Total operations checked: %d\n", len(events))
		}

		// Save visualization if requested
		if *outputFile != "" {
			var buf bytes.Buffer
			if err := porcupine.Visualize(KVModel, info, &buf); err != nil {
				log.Printf("Failed to save visualization: %v", err)
			} else {
				if err := os.WriteFile(*outputFile, buf.Bytes(), 0644); err != nil {
					log.Printf("Failed to save visualization: %v", err)
				} else {
					fmt.Printf("\nVisualization saved to: %s\n", *outputFile)
				}
			}
		}

		os.Exit(1)
	} else {
		fmt.Println("? UNKNOWN: Unable to determine linearizability")
		os.Exit(2)
	}
}

// loadHistoryFromFile loads operation history from a JSON file
func loadHistoryFromFile(path string) ([]kvstore.Operation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var history []kvstore.Operation
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}

	return history, nil
}

// fetchHistoryFromAPI fetches operation history from the KV store API
func fetchHistoryFromAPI(url string) ([]kvstore.Operation, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var history []kvstore.Operation
	if err := json.Unmarshal(body, &history); err != nil {
		return nil, err
	}

	return history, nil
}

// convertToPorcupineEvents converts our operation history to Porcupine events
func convertToPorcupineEvents(history []kvstore.Operation) []porcupine.Event {
	events := make([]porcupine.Event, 0, len(history)*2)

	for i, op := range history {
		// Determine operation type
		var opType string
		switch op.Type {
		case kvstore.OpGet:
			opType = "get"
		case kvstore.OpPut:
			opType = "put"
		default:
			continue
		}

		// Create input
		input := KVInput{
			Op:    opType,
			Key:   op.Key,
			Value: op.Value,
		}

		// Create output
		output := KVOutput{
			Value: op.Value,
			Ok:    op.Success,
		}

		// Create call event
		callEvent := porcupine.Event{
			Kind:     porcupine.CallEvent,
			Value:    input,
			Id:       i,
			ClientId: hashString(op.ClientID),
		}

		// Create return event
		returnEvent := porcupine.Event{
			Kind:     porcupine.ReturnEvent,
			Value:    output,
			Id:       i,
			ClientId: hashString(op.ClientID),
		}

		events = append(events, callEvent, returnEvent)
	}

	return events
}

// hashString creates a numeric hash from a string for client IDs
func hashString(s string) int {
	hash := 0
	for _, c := range s {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}
