package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	statsURL = "http://srv.msk01.gigacorp.local/_stats"

	// Пороговые условия
	loadAvgThreshold   = 30.0
	memUsageThreshold  = 0.80 // 80%
	diskUsageThreshold = 0.90 // 90%
	netUsageThreshold  = 0.90 // 90%

	// Бинарные единицы
	oneMiB   = 1024 * 1024
	oneMibit = 1024 * 1024 // «Mbit/s» считаем как Mebibit/s (2^20)
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
	errorMessagePrinted := false

	for {
		err := pollOnce(client)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= 3 && !errorMessagePrinted {
				fmt.Println("Unable to fetch server statistic.")
				errorMessagePrinted = true
			}
		} else {
			// при успешном чтении «сбрасываем» счётчик ошибок
			consecutiveErrors = 0
		}

		time.Sleep(interval)
	}
}

// pollOnce выполняет один запрос и печатает сообщения при превышении порогов.
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

	// Читаем тело как одну строку (Content-Type: text/plain)
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, os.ErrClosed) {
		// line может не заканчиваться \n — это нормально; ошибки чтения игнорируем,
		// если уже что-то прочитали
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return errors.New("empty body")
	}

	fields := splitCSV(line)
	if len(fields) != 7 {
		return fmt.Errorf("unexpected fields count: %d", len(fields))
	}

	// 0: Load Average (float)
	loadAvgStr := fields[0]
	loadAvg, err := strconv.ParseFloat(loadAvgStr, 64)
	if err != nil {
		return fmt.Errorf("parse load avg: %w", err)
	}

	// 1: total RAM, 2: used RAM
	totalRAM, err := parseUint(fields[1])
	if err != nil {
		return fmt.Errorf("parse total RAM: %w", err)
	}
	usedRAM, err := parseUint(fields[2])
	if err != nil {
		return fmt.Errorf("parse used RAM: %w", err)
	}

	// 3: total disk, 4: used disk
	totalDisk, err := parseUint(fields[3])
	if err != nil {
		return fmt.Errorf("parse total disk: %w", err)
	}
	usedDisk, err := parseUint(fields[4])
	if err != nil {
		return fmt.Errorf("parse used disk: %w", err)
	}

	// 5: net capacity (bytes/s), 6: net usage (bytes/s)
	netCap, err := parseUint(fields[5])
	if err != nil {
		return fmt.Errorf("parse net capacity: %w", err)
	}
	netUsed, err := parseUint(fields[6])
	if err != nil {
		return fmt.Errorf("parse net used: %w", err)
	}

	// --- Проверки и вывод сообщений ---

	// 1) Load Average
	if loadAvg > loadAvgThreshold {
		// Согласно условию — печатаем текущее значение N как есть
		fmt.Printf("Load Average is too high: %s\n", trimTrailingZeros(loadAvgStr))
	}

	// 2) Память: >80%
	if totalRAM > 0 {
		memUsage := float64(usedRAM) / float64(totalRAM)
		if memUsage > memUsageThreshold {
			percent := int(math.Round(memUsage * 100))
			fmt.Printf("Memory usage too high: %d%%\n", percent)
		}
	}

	// 3) Диск: >90% занято (т.е. свободно <10%)
	if totalDisk > 0 {
		diskUsage := float64(usedDisk) / float64(totalDisk)
		if diskUsage > diskUsageThreshold {
			freeBytes := int64(totalDisk - usedDisk)
			freeMB := freeBytes / oneMiB
			fmt.Printf("Free disk space is too low: %d Mb left\n", freeMB)
		}
	}

	// 4) Сеть: >90% занято (т.е. свободная полоса <10%)
	if netCap > 0 {
		netUsage := float64(netUsed) / float64(netCap)
		if netUsage > netUsageThreshold {
			freeBytesPerSec := int64(netCap - netUsed)
			// Переводим в Mebit/s (2^20) — целое значение
			freeMibitPerSec := (freeBytesPerSec * 8) / oneMibit
			fmt.Printf("Network bandwidth usage high: %d Mbit/s available\n", freeMibitPerSec)
		}
	}

	return nil
}

func parseUint(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	return strconv.ParseUint(s, 10, 64)
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func trimTrailingZeros(s string) string {
	// Сохраняем представление числа без лишних нулей и точки в конце
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
