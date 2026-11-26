package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/heracraft/repose/internal/api/scheduler"
)

// sampleWindows are the windows GET /projects/{id}/samples serves and the
// bucket each is drawn at: the raw minute for an hour, then wide enough
// that no window passes 288 points (DECISIONS I-492).
var sampleWindows = map[string]struct {
	span, step time.Duration
}{
	"1h":  {time.Hour, time.Minute},
	"24h": {24 * time.Hour, 5 * time.Minute},
	"7d":  {7 * 24 * time.Hour, time.Hour},
}

// sampleProcs is how many process names the samples route returns.
const sampleProcs = 8

// projectSamples serves a machine's CPU, memory, CPU pressure, host CPU
// wait and disk over a window, and its busiest process names, from the
// rows hostd already sends every minute (DECISIONS I-492, I-493). Nothing
// here is new data: meter_samples and proc_samples are what the privacy
// policy lists, shown to the project's owner.
func (s *Server) projectSamples(w http.ResponseWriter, r *http.Request) error {
	p, err := s.userProject(r)
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("window")
	if name == "" {
		name = "1h"
	}
	win, ok := sampleWindows[name]
	if !ok {
		return errf("invalid", "window must be 1h, 24h or 7d")
	}
	ctx := r.Context()
	since := time.Now().Add(-win.span)

	// A machine on a host its account owns, whose hostd shares it, has no
	// size (I-1630): it may
	// use every vCPU and all the memory of the host, so its CPU is a
	// share of the host's vCPUs and its memory is drawn against the
	// host's (I-1632). ownMilli is null on a host repose runs, where each
	// row is divided by its own class as before.
	var ownMilli *int64
	vcpus, memBytes := scheduler.ClassVCPUs(p.Class), scheduler.ClassRAM(p.Class)
	if p.HostID != nil {
		var hv int64
		var hm int64
		var arch *string
		err := s.d.Pool.QueryRow(ctx, "select vcpus, mem_bytes, arch from hosts where id = $1 and owner_account_id = $2 and $3 = any(features)", *p.HostID, p.UserID, scheduler.SharedFeature).Scan(&hv, &hm, &arch)
		if err == nil && arch != nil && *arch == "aarch64" {
			// A Mac runs its guests nested, two vCPUs each (I-1640), so
			// a machine's CPU is a share of its two: drawn against the
			// host's eight, a pegged machine read 23% (I-1654).
			hv = min(hv, nestedOwnedVCPUs)
		}
		switch {
		case err == nil && hv > 0 && hm > 0:
			m := hv * 1000
			ownMilli, vcpus, memBytes = &m, float64(hv), hm
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return err
		}
	}

	// Each row is about a minute of the guest (I-448), so a bucket's
	// fraction is its sum over count(*) minutes. CPU is divided by the
	// row's own class, so a resize mid-window draws right. A row from a
	// guest that predates I-493 has mem_used 0, and its pressure is
	// unknown rather than zero: both come back null.
	rows, err := s.d.Pool.Query(ctx, `
		select date_bin($3::interval, ts, timestamptz 'epoch') as t,
		       least(sum(cpu_ns::float8 * 1000 / coalesce($4::bigint, `+scheduler.ClassMilliVCPUSQL("class")+`)) / (count(*) * 60e9), 1),
		       avg(nullif(mem_used, 0))::bigint,
		       case when bool_or(mem_used > 0) then least(sum(cpu_pressure_us) filter (where mem_used > 0)::float8 / (count(*) filter (where mem_used > 0) * 60e6), 1) end,
		       least(sum(host_cpu_wait_us)::float8 / (count(*) * 60e6), 1),
		       max(disk_used)
		from meter_samples
		where project_id = $1 and ts > $2 and state = 'running'
		group by 1 order by 1`, p.ID, since, fmt.Sprintf("%d seconds", int(win.step.Seconds())), ownMilli)
	if err != nil {
		return err
	}
	points := []map[string]any{}
	for rows.Next() {
		var (
			t        time.Time
			cpu      float64
			mem      *int64
			pressure *float64
			wait     float64
			disk     int64
		)
		if err := rows.Scan(&t, &cpu, &mem, &pressure, &wait, &disk); err != nil {
			rows.Close()
			return err
		}
		points = append(points, map[string]any{
			"ts": t.UTC(), "cpu": cpu, "mem_used_bytes": mem, "cpu_pressure": pressure, "host_cpu_wait": wait, "disk_used_bytes": disk,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	prows, err := s.d.Pool.Query(ctx, `
		select comm, sum(cpu_ns)::float8 / 1e9, max(rss)
		from proc_samples
		where project_id = $1 and ts > $2
		group by comm order by 2 desc, comm limit $3`, p.ID, since, sampleProcs)
	if err != nil {
		return err
	}
	procs := []map[string]any{}
	for prows.Next() {
		var (
			comm string
			cpuS float64
			rss  int64
		)
		if err := prows.Scan(&comm, &cpuS, &rss); err != nil {
			prows.Close()
			return err
		}
		procs = append(procs, map[string]any{"comm": comm, "cpu_s": cpuS, "rss_max_bytes": rss})
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return err
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"window":       name,
		"step_s":       int(win.step.Seconds()),
		"vcpus":        vcpus,
		"memory_bytes": memBytes,
		"points":       points,
		"procs":        procs,
	})
	return nil
}

// nestedOwnedVCPUs is hostd's guest.OwnedNestedVCPUs: what each guest on
// a host the user owns that runs under a hypervisor boots with (I-1640).
// The api tells such a host only by its arch, aarch64 being the Mac
// appliance; an x86 host keeps the host's count.
const nestedOwnedVCPUs = 2
