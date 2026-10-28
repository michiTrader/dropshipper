// Command motor runs the Dropshipper Motor: the always-on server that answers
// WhatsApp with the AI seller, keeps orders and serves the owner's panel.
//
//	cd motor && go run ./cmd/motor
//
// Configuration: motor/.env (secrets) and motor/negocio.yaml (business).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/agent"
	"github.com/michiTrader/dropshipper/motor/internal/api"
	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/convo"
	"github.com/michiTrader/dropshipper/motor/internal/llm"
	"github.com/michiTrader/dropshipper/motor/internal/payments"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/sales"
	"github.com/michiTrader/dropshipper/motor/internal/store"
	"github.com/michiTrader/dropshipper/motor/internal/whatsapp"
)

func main() {
	dir := flag.String("dir", ".", "carpeta del Motor (donde están .env, negocio.yaml y knowledge/)")
	flag.Parse()
	lg := log.New(os.Stdout, "motor ", log.LstdFlags)
	if err := run(*dir, lg); err != nil {
		lg.Fatalf("error: %v", err)
	}
}

func run(dir string, lg *log.Logger) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	cat, err := catalog.Load(cfg.KnowledgeDir)
	if err != nil {
		return err
	}
	zones, err := rules.LoadZones(filepath.Join(cfg.KnowledgeDir, "zonas.yaml"))
	if err != nil {
		return err
	}

	var wa *whatsapp.Client
	if cfg.WhatsApp.Enabled() {
		wa = whatsapp.New(cfg.WhatsApp.Token, cfg.WhatsApp.PhoneNumberID, cfg.WhatsApp.GraphVersion)
	}
	var wompi *payments.Wompi
	if cfg.Wompi.Enabled() {
		wompi = payments.NewWompi(cfg.Wompi.PrivateKey, cfg.Wompi.EventsSecret, cfg.Wompi.Sandbox)
	}

	neg := &cfg.Negocio
	sv := &sales.Service{Store: st, Catalog: cat, Zones: zones, Negocio: neg}
	if wompi != nil {
		sv.Payments = wompi
	}
	seller := &agent.Seller{
		LLM: llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey), Model: cfg.LLM.Model, Temperature: cfg.LLM.Temperature,
		Sales: sv, Store: st, Catalog: cat, Zones: zones, Negocio: neg,
	}
	var sender convo.Sender
	if wa != nil {
		sender = wa
	}
	engine := convo.NewEngine(st, seller, sv, sender, neg, lg)
	sv.Notifier = engine
	defer engine.Close()

	srv := &api.Server{
		Cfg: cfg, Store: st, Engine: engine, Sales: sv, Catalog: cat, Zones: zones, WA: wa, Wompi: wompi,
		MediaDir: filepath.Join(cfg.DataDir, "media"), Log: lg, Started: time.Now().UTC(),
	}
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sv.FlagStalePayments(ctx)
			}
		}
	}()

	printBanner(lg, cfg, cat, wa != nil, wompi != nil)
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		lg.Printf("apagando…")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}
	return nil
}

func printBanner(lg *log.Logger, cfg *config.Config, cat *catalog.Catalog, wa, wompi bool) {
	ok := func(b bool) string {
		if b {
			return "✅"
		}
		return "⚠️ "
	}
	n := cfg.Negocio
	p := cat.Default()
	lg.Printf("Dropshipper Motor · %s · vendedora: %s", n.NombreTienda, n.Persona.Nombre)
	lg.Printf("%s IA: %s (%s)", ok(cfg.LLM.APIKey != ""), cfg.LLM.Model, cfg.LLM.BaseURL)
	lg.Printf("%s WhatsApp Cloud API", ok(wa))
	pagos := n.Pagos.Metodo
	pagosOK := (pagos == "wompi" && wompi) || (pagos == "manual" && (n.Pagos.Manual.LlaveBreB != "" || n.Pagos.Manual.NumeroCuenta != ""))
	lg.Printf("%s Pagos anticipados: %s", ok(pagosOK), pagos)
	lg.Printf("%s Producto: %s (costo confirmado: %v)", ok(p.CostConfirmed), p.Name, p.CostConfirmed)
	lg.Printf("%s Token del panel", ok(cfg.AdminToken != ""))
	lg.Printf("Panel: http://localhost:%d", cfg.Port)
	if cfg.PublicURL != "" {
		lg.Printf("Webhook WhatsApp: %s/webhook/whatsapp", cfg.PublicURL)
	}
}
