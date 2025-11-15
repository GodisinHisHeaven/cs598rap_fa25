package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

type Config struct {
	URLs            []string
	QPS             int
	Duration        time.Duration
	KeySpace        int
	ValueSize       int
	ReadRatio       float64
	Workers         int
	Timeout         time.Duration
	ReportInterval  time.Duration
	OutputFile      string
}

type Result struct {
	Op        string
	Latency   time.Duration
	Success   bool
	Timestamp time.Time
	Error     string
}

type Stats struct {
	TotalOps       int64
	SuccessfulOps  int64
	FailedOps      int64
	TotalLatency   int64
	MinLatency     int64
	MaxLatency     int64
	Latencies      []int64
	mu             sync.Mutex
}

func (s *Stats) Record(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	atomic.AddInt64(&s.TotalOps, 1)
	latencyNs := r.Latency.Nanoseconds()
	atomic.AddInt64(&s.TotalLatency, latencyNs)

	if r.Success {
		atomic.AddInt64(&s.SuccessfulOps, 1)
	} else {
		atomic.AddInt64(&s.FailedOps, 1)
	}

	s.Latencies = append(s.Latencies, latencyNs)

	// Update min/max
	if s.MinLatency == 0 || latencyNs < s.MinLatency {
		s.MinLatency = latencyNs
	}
	if latencyNs > s.MaxLatency {
		s.MaxLatency = latencyNs
	}
}

func (s *Stats) GetPercentile(p float64) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Latencies) == 0 {
		return 0
	}

	// Simple percentile calculation (not perfectly accurate but good enough)
	sorted := make([]int64, len(s.Latencies))
	copy(sorted, s.Latencies)

	// Bubble sort (inefficient but simple for small datasets)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	idx := int(float64(len(sorted)) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return time.Duration(sorted[idx])
}

func main() {
	config := parseFlags()

	log.Printf("Starting load generator")
	log.Printf("Target URLs: %v", config.URLs)
	log.Printf("QPS: %d", config.QPS)
	log.Printf("Duration: %v", config.Duration)
	log.Printf("Workers: %d", config.Workers)
	log.Printf("Key space: %d", config.KeySpace)
	log.Printf("Value size: %d bytes", config.ValueSize)
	log.Printf("Read ratio: %.2f", config.ReadRatio)

	stats := &Stats{
		Latencies: make([]int64, 0, 100000),
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received interrupt signal, shutting down...")
		cancel()
	}()

	// Start workers
	var wg sync.WaitGroup
	limiter := rate.NewLimiter(rate.Limit(config.QPS), config.QPS)

	for i := 0; i < config.Workers; i++ {
		wg.Add(1)
		go worker(ctx, &wg, i, config, stats, limiter)
	}

	// Progress reporter
	go progressReporter(ctx, stats, config.ReportInterval)

	// Wait for completion
	wg.Wait()

	// Print final results
	printFinalResults(stats, config)

	// Save results if output file specified
	if config.OutputFile != "" {
		if err := saveResults(stats, config); err != nil {
			log.Printf("Failed to save results: %v", err)
		}
	}
}

func parseFlags() *Config {
	urls := flag.String("urls", "http://localhost:8080", "Comma-separated list of target URLs")
	qps := flag.Int("qps", 100, "Queries per second")
	duration := flag.Duration("duration", 60*time.Second, "Test duration")
	keySpace := flag.Int("keys", 1000, "Number of unique keys")
	valueSize := flag.Int("value-size", 100, "Size of values in bytes")
	readRatio := flag.Float64("read-ratio", 0.5, "Ratio of read operations (0.0-1.0)")
	workers := flag.Int("workers", 10, "Number of worker goroutines")
	timeout := flag.Duration("timeout", 5*time.Second, "Request timeout")
	reportInterval := flag.Duration("report-interval", 5*time.Second, "Progress report interval")
	outputFile := flag.String("output", "", "Output file for results (JSON)")

	flag.Parse()

	// Parse URLs
	urlList := []string{*urls}

	return &Config{
		URLs:           urlList,
		QPS:            *qps,
		Duration:       *duration,
		KeySpace:       *keySpace,
		ValueSize:      *valueSize,
		ReadRatio:      *readRatio,
		Workers:        *workers,
		Timeout:        *timeout,
		ReportInterval: *reportInterval,
		OutputFile:     *outputFile,
	}
}

func worker(ctx context.Context, wg *sync.WaitGroup, id int, config *Config, stats *Stats, limiter *rate.Limiter) {
	defer wg.Done()

	client := &http.Client{
		Timeout: config.Timeout,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Rate limit
		if err := limiter.Wait(ctx); err != nil {
			return
		}

		// Decide operation type
		isRead := rand.Float64() < config.ReadRatio
		var result Result

		if isRead {
			result = doGet(client, config)
		} else {
			result = doPut(client, config)
		}

		stats.Record(result)
	}
}

func doGet(client *http.Client, config *Config) Result {
	key := fmt.Sprintf("key-%d", rand.Intn(config.KeySpace))
	url := selectURL(config.URLs) + "/kv/" + key

	start := time.Now()
	resp, err := client.Get(url)
	latency := time.Since(start)

	result := Result{
		Op:        "GET",
		Latency:   latency,
		Timestamp: start,
	}

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	io.ReadAll(resp.Body) // Consume body
	result.Success = resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound

	return result
}

func doPut(client *http.Client, config *Config) Result {
	key := fmt.Sprintf("key-%d", rand.Intn(config.KeySpace))
	value := generateValue(config.ValueSize)
	url := selectURL(config.URLs) + "/kv/" + key

	body := map[string]string{"value": value}
	bodyBytes, _ := json.Marshal(body)

	start := time.Now()
	resp, err := client.Post(url, "application/json", bytes.NewReader(bodyBytes))
	latency := time.Since(start)

	result := Result{
		Op:        "PUT",
		Latency:   latency,
		Timestamp: start,
	}

	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	io.ReadAll(resp.Body) // Consume body
	result.Success = resp.StatusCode == http.StatusOK

	return result
}

func selectURL(urls []string) string {
	if len(urls) == 1 {
		return urls[0]
	}
	return urls[rand.Intn(len(urls))]
}

func generateValue(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func progressReporter(ctx context.Context, stats *Stats, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastOps := int64(0)
	lastTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			currentOps := atomic.LoadInt64(&stats.TotalOps)
			elapsed := now.Sub(lastTime).Seconds()
			currentQPS := float64(currentOps-lastOps) / elapsed

			log.Printf("Progress: %d ops, %.1f QPS, %d successes, %d failures",
				currentOps, currentQPS,
				atomic.LoadInt64(&stats.SuccessfulOps),
				atomic.LoadInt64(&stats.FailedOps))

			lastOps = currentOps
			lastTime = now
		}
	}
}

func printFinalResults(stats *Stats, config *Config) {
	totalOps := atomic.LoadInt64(&stats.TotalOps)
	successOps := atomic.LoadInt64(&stats.SuccessfulOps)
	failedOps := atomic.LoadInt64(&stats.FailedOps)

	fmt.Println("\n=== FINAL RESULTS ===")
	fmt.Printf("Total operations: %d\n", totalOps)
	fmt.Printf("Successful: %d (%.2f%%)\n", successOps, float64(successOps)/float64(totalOps)*100)
	fmt.Printf("Failed: %d (%.2f%%)\n", failedOps, float64(failedOps)/float64(totalOps)*100)
	fmt.Printf("Actual QPS: %.2f\n", float64(totalOps)/config.Duration.Seconds())
	fmt.Println()

	if totalOps > 0 {
		avgLatency := time.Duration(atomic.LoadInt64(&stats.TotalLatency) / totalOps)
		fmt.Printf("Average latency: %v\n", avgLatency)
		fmt.Printf("Min latency: %v\n", time.Duration(stats.MinLatency))
		fmt.Printf("Max latency: %v\n", time.Duration(stats.MaxLatency))
		fmt.Printf("P50 latency: %v\n", stats.GetPercentile(50))
		fmt.Printf("P95 latency: %v\n", stats.GetPercentile(95))
		fmt.Printf("P99 latency: %v\n", stats.GetPercentile(99))
	}
}

func saveResults(stats *Stats, config *Config) error {
	results := map[string]interface{}{
		"config": map[string]interface{}{
			"qps":        config.QPS,
			"duration":   config.Duration.Seconds(),
			"workers":    config.Workers,
			"key_space":  config.KeySpace,
			"read_ratio": config.ReadRatio,
		},
		"stats": map[string]interface{}{
			"total_ops":   atomic.LoadInt64(&stats.TotalOps),
			"success_ops": atomic.LoadInt64(&stats.SuccessfulOps),
			"failed_ops":  atomic.LoadInt64(&stats.FailedOps),
			"avg_latency_ns": atomic.LoadInt64(&stats.TotalLatency) /
				max(atomic.LoadInt64(&stats.TotalOps), 1),
			"min_latency_ns": stats.MinLatency,
			"max_latency_ns": stats.MaxLatency,
			"p50_latency_ns": stats.GetPercentile(50).Nanoseconds(),
			"p95_latency_ns": stats.GetPercentile(95).Nanoseconds(),
			"p99_latency_ns": stats.GetPercentile(99).Nanoseconds(),
		},
	}

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(config.OutputFile, data, 0644)
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
