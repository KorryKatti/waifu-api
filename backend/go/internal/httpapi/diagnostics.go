package httpapi

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

// startedAt records process start, which is what uptime is measured from.
//
// Set from main rather than from an init so the clock starts when the server
// actually begins serving, not when the package is linked.
var startedAt atomic.Int64

// MarkStarted records the process start time.
func MarkStarted(t time.Time) { startedAt.Store(t.UnixNano()) }

// uptime returns how long the process has been serving, or zero if it was
// never marked.
func uptime() time.Duration {
	raw := startedAt.Load()
	if raw == 0 {
		return 0
	}
	return time.Since(time.Unix(0, raw))
}

// runtimeStats reports what the process is doing right now.
//
// Everything here comes from the runtime package and costs nothing: no
// database round trip, no filesystem walk. That matters because this is on the
// unauthenticated health endpoint, which an uptime monitor may poll often.
//
// CPU is reported as the allocated and total share rather than a percentage.
// A percentage needs a sampling interval to be meaningful, and inventing one
// here would report a number that means nothing on a single request. The two
// figures below are exact.
func runtimeStats() map[string]any {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	stats := map[string]any{
		"goVersion":     runtime.Version(),
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"numCPU":        runtime.NumCPU(),
		"numGoroutines": runtime.NumGoroutine(),

		"uptimeSeconds": uptime().Seconds(),
		"uptime":        uptime().Round(time.Second).String(),

		"memory": map[string]any{
			"allocBytes":      mem.Alloc,
			"totalAllocBytes": mem.TotalAlloc,
			"sysBytes":        mem.Sys,
			"heapAllocBytes":  mem.HeapAlloc,
			"heapSysBytes":    mem.HeapSys,
			"heapInUseBytes":  mem.HeapInuse,
			"stackInUseBytes": mem.StackInuse,
			"numGC":           mem.NumGC,
			"lastGC":          time.Unix(0, int64(mem.LastGC)).UTC().Format(time.RFC3339),
			"nextGCBytes":     mem.NextGC,
		},

		"gc": map[string]any{
			"pauseTotalNanos": mem.PauseTotalNs,
			"next":            mem.PauseNs[0],
		},
	}
	return stats
}

// systemStats adds what the operating system can tell us that the Go runtime
// does not expose.
//
// Split out from runtimeStats because it can fail: /proc is Linux-only, and
// reading it must not be able to fail a health check. Absent data is reported
// as null rather than as zero, because "0% CPU" and "could not tell" are very
// different things to an operator looking at a stalled service.
func systemStats() map[string]any {
	out := map[string]any{
		"hostname": hostname(),
		"pid":      os.Getpid(),
	}

	load1, load5, load15, ok := loadAverage()
	if ok {
		out["loadAverage"] = map[string]any{
			"1m":  load1,
			"5m":  load5,
			"15m": load15,
		}
	} else {
		out["loadAverage"] = nil
	}

	out["cpu"] = cpuUsage()
	return out
}

// cpuUsage returns process CPU usage as a fraction of one core, or null.
//
// This reads /proc/self/stat, which exists on Linux only. Render runs Linux, so
// it normally works; the null is for anywhere else.
func cpuUsage() any {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return nil
	}

	// utime and stime are fields 14 and 15, but the comm field can contain
	// spaces and is parenthesised, so parsing must start after the last ')'.
	idx := lastIndexByte(string(raw), ')')
	if idx < 0 {
		return nil
	}
	fields := splitFields(string(raw)[idx+1:])
	// After the closing paren, field 1 is state. utime is the 12th token from
	// there, stime the 13th.
	if len(fields) < 13 {
		return nil
	}

	utime, ok1 := parseUint(fields[11])
	stime, ok2 := parseUint(fields[12])
	if !ok1 || !ok2 {
		return nil
	}

	// Clock ticks are conventionally 100 per second, which is what every
	// Linux userland assumes and what runtime uses internally.
	const ticksPerSecond = 100
	ticks := utime + stime
	return map[string]any{
		"ticks":       ticks,
		"secondsUsed": float64(ticks) / ticksPerSecond,
	}
}

func loadAverage() (f1, f5, f15 float64, ok bool) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, false
	}
	a, b, c, ok := parseThreeFloats(string(raw))
	return a, b, c, ok
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// buildInfo reports what went into the binary, which is the first thing asked
// when a deployed version misbehaves.
//
// The identity comes from the link-time stamps in version.go rather than from
// debug.ReadBuildInfo, so /healthz and /version cannot report two different
// answers to "which version is this". ReadBuildInfo is still consulted for the
// toolchain version and for vcs.revision / vcs.time, which the linker records
// only when the build happened in a VCS checkout — a local `go build`, not the
// Docker image, where .git is outside the build context.
func buildInfo() map[string]any {
	info := map[string]any{
		"startedAt": time.Unix(0, startedAt.Load()).UTC().Format(time.RFC3339),
		"version":   BuildVersion,
		"commit":    BuildCommit,
		"builtAt":   BuildAt,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info["goVersion"] = bi.GoVersion
		for _, setting := range bi.Settings {
			if setting.Key == "vcs.revision" {
				info["vcsRevision"] = setting.Value
			}
			if setting.Key == "vcs.time" {
				info["vcsTime"] = setting.Value
			}
		}
	}
	return info
}

// string renders the stats map for logs and debugging.
func statsString(stats map[string]any) string { return fmt.Sprint(stats) }

// lastIndexByte returns the index of the final occurrence of c.
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// splitFields splits on runs of whitespace, which is how /proc formats its
// files. Empty fields cannot occur, unlike with strings.Fields on empty input.
func splitFields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\n' || s[i] == '\t' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// parseUint parses an unsigned decimal, reporting failure rather than zero.
func parseUint(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// parseThreeFloats reads the first three whitespace-separated floats, which is
// the shape of /proc/loadavg.
func parseThreeFloats(s string) (float64, float64, float64, bool) {
	fields := splitFields(s)
	if len(fields) < 3 {
		return 0, 0, 0, false
	}
	var out [3]float64
	for i := range out {
		f, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return 0, 0, 0, false
		}
		out[i] = f
	}
	return out[0], out[1], out[2], true
}
