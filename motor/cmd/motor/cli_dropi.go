package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/api"
	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/dropi"
	"github.com/michiTrader/dropshipper/motor/internal/geo"
	"github.com/michiTrader/dropshipper/motor/internal/hub"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/settings"
	"github.com/michiTrader/dropshipper/motor/internal/shipping"
)

// apiServer builds the same logic the panel uses (quote, simulate, costs) so
// the terminal and the panel can never disagree.
func (c *cli) apiServer() (*api.Server, error) {
	zones, err := rules.LoadZones(filepath.Join(c.cfg.KnowledgeDir, "zonas.yaml"))
	if err != nil {
		return nil, err
	}
	h := hub.New()
	h.Manager = c.set
	h.Apply(c.set.Get())
	live := config.NewLive(c.set.Get().Negocio)
	ship := &shipping.Service{
		Negocio: live,
		Client: func() shipping.Quoter {
			if cl := h.Dropi(); cl != nil {
				return cl
			}
			return nil
		},
		Origin: func() (string, string) { d := h.Settings().Dropi; return d.OriginDept, d.OriginCity },
	}
	return &api.Server{Settings: c.set, Hub: h, Store: c.st, Catalog: c.cat, Zones: zones, Shipping: ship}, nil
}

func modeLabel(sandbox bool) string {
	if sandbox {
		return "pruebas (test-api.dropi.co, sin transportadoras)"
	}
	return "produccion (api.dropi.co)"
}

// ---------- dropi ----------

func (c *cli) dropi(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	sub, rest := args[0], args[1:]
	s := c.set.Get()
	d := s.Dropi
	switch sub {
	case "ver":
		c.p("Conectado:         %s", yesNo(d.Enabled()))
		switch {
		case d.UsesMCP() && d.MCPStale:
			c.p("Inicio de sesion:  VENCIO: entra al panel > Conexiones > Dropi > Conectar con Dropi")
		case d.UsesMCP():
			c.p("Inicio de sesion:  si (MCP de Dropi), desde %s", d.MCPSince.Local().Format("2006-01-02 15:04"))
		default:
			c.p("Inicio de sesion:  no (se conecta desde el panel: Conexiones > Dropi > Conectar con Dropi)")
		}
		c.p("Token (avanzado):  %s", maskOr(d.Token))
		c.p("Entorno:           %s", modeLabel(d.Sandbox))
		c.p("Origen:            %s", firstOr(strings.TrimSpace(d.OriginCity+", "+d.OriginDept), "(sin configurar: motor dropi configurar --ciudad-origen ...)"))
		c.p("Modo de pedidos:   %s (manual = tu copias el pedido a Dropi; api = el Motor lo crea)", s.Negocio.Dropi.Modo)
		return nil
	case "mcp":
		return c.dropiMCP(rest)
	case "quitar":
		if d.UsesMCP() && d.MCPRevokeURL != "" {
			rctx, cancel := context.WithTimeout(c.ctx, 8*time.Second)
			_ = dropi.Revoke(rctx, nil, d.MCPRevokeURL, d.MCPClientID, firstOr(d.MCPRefresh, d.MCPAccess))
			cancel()
		}
		if err := c.set.Update(c.ctx, func(st *settings.Settings) error {
			st.Dropi = config.DropiConfig{Sandbox: st.Dropi.Sandbox, OriginDept: st.Dropi.OriginDept, OriginCity: st.Dropi.OriginCity}
			st.Negocio.Dropi.Modo = "manual"
			return nil
		}); err != nil {
			return err
		}
		c.applied("Dropi desconectado; los pedidos vuelven a modo manual.")
		return nil
	case "configurar":
		fs := flag.NewFlagSet("dropi", flag.ContinueOnError)
		tok := fs.String("token", "", "")
		dept := fs.String("departamento-origen", "", "")
		city := fs.String("ciudad-origen", "", "")
		prod := fs.Bool("produccion", false, "")
		test := fs.Bool("pruebas", false, "")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if *prod && *test {
			return errors.New("elige --produccion o --pruebas, no los dos")
		}
		seen := visited(fs)
		err := c.set.Update(c.ctx, func(st *settings.Settings) error {
			var err error
			if seen["token"] {
				if st.Dropi.Token, err = secret(*tok); err != nil {
					return err
				}
			}
			if seen["departamento-origen"] {
				st.Dropi.OriginDept = strings.TrimSpace(*dept)
			}
			if seen["ciudad-origen"] {
				st.Dropi.OriginCity = strings.TrimSpace(*city)
			}
			if st.Dropi.OriginCity != "" {
				if _, ok := geo.Find(st.Dropi.OriginDept, st.Dropi.OriginCity); !ok {
					return fmt.Errorf("no reconozco la ciudad de origen %q; escribela como aparece en Dropi (ej.: Bogota, Medellin)", st.Dropi.OriginCity)
				}
			}
			if *prod {
				st.Dropi.Sandbox = false
			}
			if *test {
				st.Dropi.Sandbox = true
			}
			if st.Dropi.Enabled() {
				st.Negocio.Dropi.Modo = "api"
			}
			return nil
		})
		if err != nil {
			return err
		}
		c.applied("Dropi configurado. Verifica con: motor dropi probar")
		return nil
	case "probar":
		if !d.Enabled() {
			return errors.New("Dropi no esta conectado: abre el panel > Conexiones > Dropi > Conectar con Dropi (o, avanzado: echo $TOKEN | motor dropi configurar --token -)")
		}
		ctx, cancel := context.WithTimeout(c.ctx, 25*time.Second)
		defer cancel()
		if d.UsesMCP() {
			cl, err := c.liveDropi()
			if err != nil {
				return err
			}
			n, err := cl.Ping(ctx)
			if err != nil {
				return errors.New(api.DropiAdvice(err))
			}
			c.p("Conexion correcta con Dropi por tu inicio de sesion (%d herramientas).", n)
			return nil
		}
		n, err := dropi.New(d.Token, d.Sandbox).Ping(ctx)
		if err != nil {
			return err
		}
		c.p("Conexion correcta con Dropi (%d departamentos).", n)
		if d.Sandbox {
			c.p("Aviso: el entorno de pruebas no tiene transportadoras; no puede cotizar fletes.")
		}
		if d.OriginCity == "" {
			c.p("Aviso: falta la ciudad de origen (motor dropi configurar --ciudad-origen ...).")
		}
		return nil
	case "cotizar", "simular":
		return c.dropiScenario(sub, rest)
	case "costos":
		if len(rest) != 1 {
			return errors.New("uso: motor dropi costos SKU   (el producto necesita su ID de Dropi)")
		}
		srv, err := c.apiServer()
		if err != nil {
			return err
		}
		out, err := srv.PullCosts(c.ctx, rest[0])
		if err != nil {
			return err
		}
		c.p("Costo actualizado desde Dropi: $%s (antes $%s). Marcado como confirmado.",
			catalog.FormatCOP(out["costo"].(int64)), catalog.FormatCOP(out["costo_anterior"].(int64)))
		if w, _ := out["flete_aviso"].(string); w != "" {
			c.p("Aviso: el flete NO se pudo confirmar: %s", w)
		} else if n, _ := out["flete_ciudades"].(int); n > 0 {
			c.p("Flete real de Dropi: $%s (antes $%s), promedio del mas barato hacia %d ciudades principales.",
				catalog.FormatCOP(out["flete"].(int64)), catalog.FormatCOP(out["flete_anterior"].(int64)), n)
		}
		return nil
	}
	return fmt.Errorf("no conozco 'dropi %s'; usa 'motor ayuda'", sub)
}

// liveDropi builds the Dropi client the way the Motor does (the same login,
// renewed sessions are saved back).
func (c *cli) liveDropi() (dropi.API, error) {
	srv, err := c.apiServer()
	if err != nil {
		return nil, err
	}
	cl := srv.Hub.Dropi()
	if cl == nil {
		return nil, errors.New("Dropi no esta conectado")
	}
	return cl, nil
}

// dropiMCP: "motor dropi mcp herramientas" lists what Dropi's MCP offers and
// whether the Motor can use it; "motor dropi mcp llamar HERRAMIENTA JSON" runs
// one tool by hand (read-only tools only, please).
func (c *cli) dropiMCP(args []string) error {
	cl, err := c.liveDropi()
	if err != nil {
		return err
	}
	m, ok := cl.(*dropi.MCPAPI)
	if !ok {
		return errors.New("esto requiere el inicio de sesion con Dropi (panel > Conexiones > Dropi > Conectar con Dropi)")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
	defer cancel()
	sub := "herramientas"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "herramientas":
		tools, rep, err := m.Report(ctx)
		if err != nil {
			return errors.New(api.DropiAdvice(err))
		}
		c.p("Herramientas del MCP de Dropi (%d):", len(tools))
		for _, t := range tools {
			c.p("  - %s: %s", t.Name, snippetText(t.Description, 100))
		}
		c.p("")
		for _, r := range rep {
			mark := "NO "
			if r.Ready {
				mark = "OK "
			}
			c.p("%s%s -> %s: %s", mark, r.Job, firstOr(r.Tool, "(ninguna)"), snippetText(r.Detail, 300))
		}
		return nil
	case "esquemas":
		// Full input schema of every tool: the exact field names Dropi expects.
		tools, err := m.Tools(ctx)
		if err != nil {
			return errors.New(api.DropiAdvice(err))
		}
		b, _ := json.MarshalIndent(tools, "", " ")
		c.p("%s", string(b))
		return nil
	case "registro":
		// Makes a real (read only) quote and prints what was sent and answered.
		srv, err := c.apiServer()
		if err != nil {
			return err
		}
		dept, city := "Antioquia", "Medellin"
		if len(args) > 2 {
			dept, city = args[1], args[2]
		}
		out, qerr := srv.Quote(ctx, api.ScenarioIn{Dept: dept, City: city})
		if qerr != nil {
			c.p("La cotizacion fallo: %s", qerr.Error())
		} else {
			j, _ := json.Marshal(out.Comparison)
			c.p("Cotizacion: %s", string(j))
		}
		for _, l := range m.CallLog() {
			c.p("")
			c.p("[%s] %s (%d ms)", l.At.Format("15:04:05"), l.Tool, l.Millis)
			c.p("  ENVIADO:   %s", l.Args)
			c.p("  RESPUESTA: %s", l.Result)
			if l.Err != "" {
				c.p("  ERROR:     %s", l.Err)
			}
		}
		return nil
	case "llamar":
		if len(args) < 2 {
			return errors.New("uso: motor dropi mcp llamar HERRAMIENTA '{\"campo\":\"valor\"}'")
		}
		var in map[string]any
		if len(args) > 2 {
			if err := json.Unmarshal([]byte(args[2]), &in); err != nil {
				return fmt.Errorf("el JSON de argumentos no es valido: %w", err)
			}
		}
		res, err := m.Call(ctx, args[1], in)
		if err != nil {
			return errors.New(api.DropiAdvice(err))
		}
		c.p("%s", string(res.JSON()))
		if res.IsError {
			return errors.New("la herramienta respondio con error")
		}
		return nil
	}
	return errors.New("uso: motor dropi mcp [herramientas | esquemas | registro [DEPARTAMENTO CIUDAD] | llamar HERRAMIENTA JSON]")
}

func snippetText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func firstOr(v, def string) string {
	if strings.Trim(v, ", ") == "" {
		return def
	}
	return v
}

func (c *cli) dropiScenario(sub string, rest []string) error {
	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	sku := fs.String("producto", "", "")
	offer := fs.String("oferta", "", "")
	dept := fs.String("departamento", "", "")
	city := fs.String("ciudad", "", "")
	hood := fs.String("barrio", "", "")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, rest); err != nil {
		return err
	}
	srv, err := c.apiServer()
	if err != nil {
		return err
	}
	in := api.ScenarioIn{SKU: *sku, OfferID: *offer, Dept: *dept, City: *city, Hood: *hood}
	ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
	defer cancel()
	if sub == "cotizar" {
		out, err := srv.Quote(ctx, in)
		if err != nil {
			return err
		}
		if *asJSON {
			return c.json(out)
		}
		c.p("%s - %s - valor $%s - cobro: %s", out.Product, out.Offer, catalog.FormatCOP(out.Value), out.Shipping.Policy)
		c.printComparison(out.Comparison)
		if !out.CostConfirmed {
			c.p("Aviso: el costo del producto esta SIN CONFIRMAR; las ganancias son una estimacion.")
		}
		return nil
	}
	out, err := srv.Simulate(ctx, in)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.json(out)
	}
	c.p("%s", out.Message)
	for _, ch := range out.Checks {
		tag := map[string]string{"ok": "[ok]    ", "aviso": "[aviso] ", "error": "[error] "}[ch.Status]
		c.p("%s%s: %s", tag, ch.Name, ch.Detail)
	}
	c.p("")
	c.printComparison(out.Comparison)
	b, _ := json.MarshalIndent(out.Payload, "", "  ")
	c.p("\nPedido que se enviaria a Dropi (no se envia):\n%s", b)
	if !out.OK {
		return errors.New("la simulacion encontro errores (arriba)")
	}
	return nil
}

func (c *cli) json(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	c.p("%s", b)
	return nil
}

func (c *cli) printComparison(cmp shipping.Comparison) {
	if len(cmp.Options) == 0 {
		c.p("Sin transportadoras activas para comparar (motor transportadoras ver).")
		return
	}
	w := c.tw()
	fmt.Fprintln(w, "TRANSPORTADORA\tFLETE\tGANANCIA SI ENTREGA\tPERDIDA SI DEVUELVE\tESPERADA\tDEVOLUCION\t")
	for _, o := range cmp.Options {
		mark := ""
		if o.Best {
			mark = "<- mejor"
		}
		fmt.Fprintf(w, "%s\t$%s\t$%s\t$%s\t$%s\t%d%%\t%s\n", o.Carrier, catalog.FormatCOP(o.Freight), catalog.FormatCOP(o.ProfitIfDelivered),
			catalog.FormatCOP(abs(o.LossIfReturned)), catalog.FormatCOP(o.ExpectedProfit), int(o.ReturnRate*100+0.5), mark)
	}
	_ = w.Flush()
	src := "cotizado en Dropi"
	if cmp.Source != shipping.SourceDropi {
		src = "ESTIMADO (flete del producto), no es el precio de Dropi"
	}
	c.p("Fuente del flete: %s", src)
	if cmp.Warning != "" {
		c.p("Aviso: %s", cmp.Warning)
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------- transportadoras ----------

func (c *cli) transportadoras(args []string) error {
	if len(args) == 0 {
		args = []string{"ver"}
	}
	sub, rest := args[0], args[1:]
	if sub == "ver" {
		w := c.tw()
		fmt.Fprintln(w, "ID\tNOMBRE\tACTIVA\tDEVOLUCION\tPRIORIDAD\tNOTAS")
		for _, t := range c.set.Get().Negocio.Transportadoras {
			tasa := "(general)"
			if t.TasaDevolucion > 0 {
				tasa = strconv.FormatFloat(t.TasaDevolucion*100, 'f', -1, 64) + "%"
			}
			n := strings.ReplaceAll(t.Notas, "\n", " ")
			if len([]rune(n)) > 50 {
				n = string([]rune(n)[:50]) + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", t.ID, t.Nombre, yesNo(t.Activa), tasa, t.Prioridad, n)
		}
		return w.Flush()
	}
	if len(rest) == 0 {
		return fmt.Errorf("uso: motor transportadoras %s ID ...", sub)
	}
	id, vals := rest[0], rest[1:]
	var msg string
	err := c.set.Update(c.ctx, func(st *settings.Settings) error {
		t, ok := st.Negocio.Carrier(id)
		if !ok {
			return fmt.Errorf("no existe la transportadora %q (motor transportadoras ver)", id)
		}
		idx := -1
		for i := range st.Negocio.Transportadoras {
			if st.Negocio.Transportadoras[i].ID == t.ID {
				idx = i
			}
		}
		tp := &st.Negocio.Transportadoras[idx]
		switch sub {
		case "activar":
			tp.Activa, msg = true, tp.Nombre+" activada: la IA y el selector ya la consideran."
		case "desactivar":
			tp.Activa, msg = false, tp.Nombre+" desactivada: no se usara para nuevos pedidos."
		case "notas":
			tp.Notas = strings.TrimSpace(strings.Join(vals, " "))
			if tp.Notas == "-" {
				tp.Notas = ""
			}
			msg = "Notas de " + tp.Nombre + " guardadas: la IA las lee en su prompt."
		case "tasa":
			if len(vals) != 1 {
				return errors.New("uso: motor transportadoras tasa ID 18%   (0 = usar la tasa general)")
			}
			x, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(vals[0], "%"), ",", "."), 64)
			if err != nil || x < 0 || x > 100 {
				return fmt.Errorf("%q no es un porcentaje entre 0 y 100", vals[0])
			}
			tp.TasaDevolucion, msg = x/100, "Tasa de devolucion de "+tp.Nombre+" actualizada."
		case "prioridad":
			if len(vals) != 1 {
				return errors.New("uso: motor transportadoras prioridad ID N   (menor = se prefiere en empates)")
			}
			n, err := strconv.Atoi(vals[0])
			if err != nil {
				return fmt.Errorf("%q no es un numero entero", vals[0])
			}
			tp.Prioridad, msg = n, "Prioridad de "+tp.Nombre+" actualizada."
		default:
			return fmt.Errorf("no conozco 'transportadoras %s'; usa 'motor ayuda'", sub)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.applied(msg)
	return nil
}
