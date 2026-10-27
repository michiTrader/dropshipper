# Dropshipper

Equipo de agentes de IA para dropshipping contraentrega en Colombia. Primer producto: **LullaBites** 🌙.

## Estado: Fase 1, Vendedor IA por WhatsApp ✅ (código listo, falta conectar tus cuentas)

El **Motor** (`motor/`) es un solo programa en Go que:
- atiende WhatsApp con **Sofi**, la vendedora IA (DeepSeek), con el playbook de ventas y la ficha de LullaBites;
- toma los datos de envío y decide **contraentrega o anticipo** según `knowledge/zonas.yaml`;
- cobra anticipos por **llave Bre-B / Bancolombia** (verificas el comprobante) o por **Wompi** (automático);
- deja cada pedido en **Aprobaciones** con su rentabilidad (incluye el riesgo de devolución) para que des **Aceptar**;
- permite **✋ Yo tomo** en cualquier chat y registra **por qué** la IA hizo cada cosa;
- trae un **panel web** (escritorio y celular, instalable como app) y un **simulador** para probar sin WhatsApp.

```bash
cd motor
cp .env.example .env     # pon LLM_API_KEY (DeepSeek) y ADMIN_TOKEN
go run ./cmd/motor       # abre http://localhost:8080
```

📘 Guía paso a paso (WhatsApp, Bre-B, Dropi): [`docs/CONFIGURACION.md`](docs/CONFIGURACION.md)
📄 Propuesta completa: [`docs/PROPUESTA.md`](docs/PROPUESTA.md)
🧠 Por qué Arxi todavía no es el motor de agentes: [`docs/DECISION-ARXI.md`](docs/DECISION-ARXI.md)

## Estructura

```
motor/
├── cmd/motor/            # programa principal
├── internal/
│   ├── agent/            # Sofi: loop de herramientas sobre la API compatible con OpenAI
│   ├── sales/            # ciclo de vida del pedido (la IA nunca decide dinero)
│   ├── rules/            # zonas/anticipos y rentabilidad con devoluciones (con pruebas)
│   ├── convo/            # orquestador: agrupar mensajes, Yo tomo, comprobantes, eventos en vivo
│   ├── whatsapp/         # WhatsApp Cloud API oficial + verificación de firma
│   ├── payments/         # Wompi: links de pago + verificación de eventos
│   ├── store/            # SQLite (sin CGO, compila igual en Windows)
│   └── api/              # API HTTP + panel web embebido
├── knowledge/            # playbook de ventas, ficha de LullaBites, reglas de zona
└── negocio.yaml          # tienda, persona, pagos, autonomía, finanzas
```

## Pruebas

```bash
cd motor && go test ./...
```
Cubren las reglas de zona y rentabilidad, la firma de Wompi y WhatsApp, el cliente de IA y el flujo de venta
completo (contraentrega en Medellín, anticipo en zona lejana, zona roja, Yo tomo, escalamiento, mensajes agrupados
y cambios después de confirmar).
