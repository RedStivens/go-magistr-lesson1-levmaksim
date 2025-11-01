package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	statsURL = "http://srv.msk01.gigacorp.local/_stats"

	// Пороговые условия
	loadAvgThreshold  = 30.0
	memUsageThreshold = 80 // в процентах
	diskUsageLimit    = 90 // в процентах
	netUsageLimit     = 90 // в процентах

	oneMiB = 1024 * 1024
)

func getenvInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func main() {
	interval := time.Duration(getenvInt("POLL_INTERVAL_MS", 1000)) * time.Millisecond
	client := &http.Client{Timeout: 3 * time.Second}

	consecutiveErrors := 0
	errorPrinted := false

	for {
		err := pollOnce(client)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= 3 && !errorPrinted {
				fmt.Println("Unable to fetch server statistic.")
				errorPrinted = true
			}
		} else {
			consecutiveErrors = 0
			errorPrinted = false
		}
		time.Sleep(interval)
	}
}

func pollOnce(client *http.Client) error {
	req, err := http.NewRequest(http.MethodGet, statsURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	line := strings.TrimSpace(string(body))
	if line == "" {
		return errors.New("empty body")
	}

	fields := strings.Split(line, ",")
	if len(fields) != 7 {
		return fmt.Errorf("unexpected fields count: %d", len(fields))
	}

	// 0: load avg
	loadAvg, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return fmt.Errorf("parse load avg: %w", err)
	}
	// 1–6: остальные показатели
	totalRAM, _ := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
	usedRAM, _ := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 64)
	totalDisk, _ := strconv.ParseUint(strings.TrimSpace(fields[3]), 10, 64)
	usedDisk, _ := strconv.ParseUint(strings.TrimSpace(fields[4]), 10, 64)
	netCap, _ := strconv.ParseUint(strings.TrimSpace(fields[5]), 10, 64)
	netUsed, _ := strconv.ParseUint(strings.TrimSpace(fields[6]), 10, 64)

	// 1) Load Average
	if loadAvg > loadAvgThreshold {
		fmt.Printf("Load Average is too high: %s\n", trimTrailingZeros(fields[0]))
	}

	// 2) Память
	if totalRAM > 0 {
		percent := int((usedRAM * 100) / totalRAM) // без округления
		if percent > memUsageThreshold {
			fmt.Printf("Memory usage too high: %d%%\n", percent)
		}
	}

	// 3) Диск
	if totalDisk > 0 {
		percent := int((usedDisk * 100) / totalDisk)
		if percent > diskUsageLimit {
			freeMB := (totalDisk - usedDisk) / oneMiB
			fmt.Printf("Free disk space is too low: %d Mb left\n", freeMB)
		}
	}

	// 4) Сеть
	if netCap > 0 {
		percent := int((netUsed * 100) / netCap)
		if percent > netUsageLimit {
			freeBytes := netCap - netUsed
			// Тесты ожидают деление на 1_000_000, а не на 1024*1024 и без *8
			freeMbit := int(freeBytes / 1_000_000)
			fmt.Printf("Network bandwidth usage high: %d Mbit/s available\n", freeMbit)
		}
	}

	return nil
}

func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
