// Command whenweall is the application binary: it serves the HTTP API and SPA, runs database
// migrations, and answers the Docker HEALTHCHECK (which has no shell to work with inside the
// scratch image, so the healthcheck is a subcommand of this same binary).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "time/tzdata" // scratch has no /usr/share/zoneinfo; scheduling needs real tz data.

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/rooms"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	cmd := "serve"
	if len(args) > 1 {
		cmd = args[1]
	}
	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		return migrateCmd()
	case "healthcheck":
		return healthcheck()
	case "create-staff-user":
		return createStaffUserCmd(args[2:])
	case "version":
		fmt.Println(version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve|migrate|healthcheck|create-staff-user|version)\n", cmd)
		return 2
	}
}

// serve loads configuration, opens the database, migrates on boot if configured to, and runs
// the HTTP server until SIGINT/SIGTERM triggers a graceful shutdown.
func serve() int {
	cfg, warnings, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = sqlDB.Close() }()

	if cfg.MigrateOnBoot {
		if err := db.Migrate(ctx, sqlDB); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	// replicaID identifies this process to ClaimDue's locked_by column — it only needs to be
	// unique among concurrently running replicas, not stable across restarts.
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "replica"
	}
	worker := jobs.NewWorker(sqlDB, hostname+"-"+db.NewID()[:6], slog.Default())
	m := mailer.New(cfg)
	m.RegisterHandler(worker)
	// rooms.BroadcastPresenceTotal is a package-level function, not tied to any particular Hub
	// instance, so it can be wired in here even though the Hub itself isn't constructed until
	// below — see jobs.RegisterHousekeeping's own doc comment for why this indirection exists at
	// all (internal/jobs cannot import internal/rooms directly without an import cycle).
	jobs.RegisterHousekeeping(worker, sqlDB, rooms.BroadcastPresenceTotal)

	// hub is this replica's realtime fan-out (internal/rooms, plan 6): it owns its own dedicated
	// LISTEN session (Run) independent of sqlDB's pooled connections, so it's started here,
	// before anything below registers a WS route against it. statsSvc is the landing-page
	// counters room (also plan 6) — wired into pollsSvc via SetStats before pollsSvc's own
	// mutating methods (Create/Finalize/AddParticipant/...) ever run, so no request can reach a
	// half-wired Service.
	hub := rooms.NewHub(cfg.DatabaseURL, sqlDB, slog.Default())
	// OriginPatterns (M8, hub.go's own doc comment): allow-list the app's own configured origin
	// for the WS handshake's Origin check, on top of (never instead of) Accept's default check
	// against the request's own Host header — config.Load has already validated cfg.AppURL as an
	// absolute http(s) URL with a non-empty host by the time serve() ever reaches this line, so
	// url.Parse succeeding is not defensively re-checked here.
	if u, err := url.Parse(cfg.AppURL); err == nil && u.Host != "" {
		hub.OriginPatterns = []string{u.Host}
	}
	// bg tracks the two long-lived background goroutines (hub.Run, worker.Run) so serve() can wait
	// for them to unwind — the hub closing its WebSockets with a GoingAway frame and clearing this
	// replica's presence rows, the worker finishing its current poll — BEFORE the deferred
	// sqlDB.Close() above pulls the pool out from under them. Run always returns ctx.Err() (never
	// nil, per its own doc comment) — on an ordinary SIGINT/SIGTERM that's just context.Canceled,
	// not a failure worth logging; Run's own internal logging already covers every real
	// connection/LISTEN problem along the way.
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		_ = hub.Run(ctx)
	}()
	statsSvc := rooms.NewStatsService(sqlDB, slog.Default())

	// pollsSvc owns the poll/sign-up-sheet domain: its HTTP surface (Register, below) and its
	// three scheduled job kinds (poll.deadline/poll.digest/mail:poll — RegisterJobs) share this
	// one instance, bound to the same *sql.DB the rest of the process uses.
	pollsSvc := polls.NewService(sqlDB)
	pollsSvc.SetStats(statsSvc)
	pollsSvc.RegisterJobs(worker, m)

	// bookingsSvc owns the booking-page domain: its HTTP surface (Register, below) and its own
	// three scheduled job kinds (booking.reminder/mail:booking/google:sync — RegisterJobs) share
	// this one instance, the same shape pollsSvc uses above. SetGoogleSync wires in a real Google
	// Calendar client (nil — sync off — when the capability itself isn't configured; see
	// NewGoogleSync's own doc comment), BEFORE RegisterJobs so "google:sync" jobs the worker picks
	// up immediately after Run starts see a fully wired Service, never a half-built one.
	bookingsSvc := bookings.NewService(cfg, sqlDB)
	bookingsSvc.SetGoogleSync(bookings.NewGoogleSync(cfg, sqlDB))
	bookingsSvc.RegisterJobs(worker, m)

	authSvc, err := auth.New(cfg, sqlDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Plan C: user recipients' mail locale (user_preferences via auth.Service.LocaleFor). Wired
	// before worker.Run so the first mail:poll job the worker claims already sees it.
	pollsSvc.SetLocaleSource(authSvc)

	if err := jobs.EnsureScheduled(ctx, sqlDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	bg.Add(1)
	go func() {
		defer bg.Done()
		worker.Run(ctx)
	}()

	srv := httpserver.New(cfg, sqlDB, authSvc)
	srv.RegisterAPI(func(mux *http.ServeMux) {
		pollsSvc.Register(mux, authSvc, cfg)
		bookingsSvc.Register(mux, authSvc, cfg)
		rooms.Register(mux, hub, authSvc, pollsSvc, bookingsSvc, statsSvc, cfg)
		admin.Register(mux, authSvc, sqlDB)
		// Task 5: the e2e seed endpoint Playwright's fixtures call, gated the same way
		// config.Load itself hard-fails boot on (EnableTestRoutes set alongside
		// APP_ENV=production) — never reachable in a real deployment.
		if cfg.EnableTestRoutes {
			httpserver.RegisterTestRoutes(mux, cfg, authSvc, pollsSvc, bookingsSvc)
		}
	})
	serveErr := srv.ListenAndServe(ctx)
	// Whether ListenAndServe returned because ctx ended (SIGTERM — the hub and worker are already
	// unwinding) or because the listener itself failed (they are not), the background goroutines
	// stop the same way: cancel, then wait for them to actually exit. A job in flight at this
	// moment sees its ctx cancelled and is retried later by the at-least-once queue (5-minute lock
	// expiry) — an accepted trade for never holding shutdown hostage to a two-minute JobTimeout.
	cancel()
	bg.Wait()
	if serveErr != nil {
		fmt.Fprintln(os.Stderr, serveErr)
		return 1
	}
	return 0
}

// migrateCmd loads configuration, applies pending migrations, and exits — no server started.
func migrateCmd() int {
	cfg, warnings, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	ctx := context.Background()
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = sqlDB.Close() }()

	if err := db.Migrate(ctx, sqlDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// createStaffUserCmd loads configuration, opens the database, and flags the given email's user
// as platform staff (see auth.Service.MakeStaff) — the bootstrap path for granting the first
// staff account, replacing the old seed-script approach. Idempotent: re-running against an
// already-staffed email succeeds silently.
func createStaffUserCmd(args []string) int {
	fs := flag.NewFlagSet("create-staff-user", flag.ContinueOnError)
	email := fs.String("email", "", "email of the user to flag as platform staff (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *email == "" {
		fmt.Fprintln(os.Stderr, "create-staff-user: --email is required")
		return 1
	}

	cfg, warnings, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	ctx := context.Background()
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = sqlDB.Close() }()

	authSvc, err := auth.New(cfg, sqlDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := authSvc.MakeStaff(ctx, *email); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("%s is now a staff user\n", *email)
	return 0
}

// healthcheck is the Docker HEALTHCHECK entry point: the scratch image has no shell, so `CMD
// ["/whenweall", "healthcheck"]` invokes this instead of curl/wget.
func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
