# Dropshipper

Equipo de agentes de IA para dropshipping contraentrega en Colombia. Primer producto: **LullaBites**.

## Estado: Fase 1, Vendedor IA por WhatsApp (codigo listo, falta conectar tus cuentas)

El **Motor** (`motor/`) es un solo programa en Go que:
- atiende WhatsApp con una vendedora IA que usa **cualquier proveedor compatible con OpenAI** (DeepSeek, OpenAI, OpenRouter, Groq, Gemini, Ollama local, Arxi u otro: basta URL, modelo y clave);
- vende **varios productos**, cada uno con su ficha, ofertas, costos y finanzas;
- toma los datos de envio y decide **contraentrega o anticipo** segun `knowledge/zonas.yaml`;
- cobra anticipos por **llave Bre-B / cuenta bancaria** (verificas el comprobante) o por **Wompi** (automatico);
- deja cada pedido en **Aprobaciones** con su rentabilidad (incluye el riesgo de devolucion) para que lo apruebes;
- permite **tomar el control** de cualquier chat y registra **por que** la IA hizo cada cosa;
- trae un **panel web** (escritorio y celular, instalable) y un **simulador** para probar sin WhatsApp.

## Todo se conecta desde el panel o desde la terminal

Las dos vias editan **los mismos datos** (base de datos del Motor). Un Motor encendido ve los cambios hechos
desde la terminal en pocos segundos, sin reiniciar. No hace falta editar archivos ni variables de entorno.

```bash
cd motor
go run ./cmd/motor            # enciende el Motor: abre http://localhost:8080
```

En el panel: **Conexiones** (IA, WhatsApp, cobros, seguridad, cada una con boton de prueba),
**Productos** (lista, detalle y finanzas por producto) y **Ajustes** (persona de la IA, autonomia,
supuestos financieros). Cada bloque trae un desplegable "Hacerlo desde la terminal" con los comandos equivalentes.

Desde la terminal (`go build -o motor ./cmd/motor` y luego `./motor ...`):

```bash
motor estado                                   # que esta conectado y que falta
echo $CLAVE | motor ia agregar --preset deepseek --clave - --activar
motor ia probar                                # llamada real de prueba
motor ia agregar --nombre Arxi --url https://tu-arxi/v1 --modelo mi-modelo --clave - --activar   # la URL suele terminar en /v1
echo $TOKEN | motor whatsapp configurar --token - --telefono-id 123456789 --verify-token mi-texto
motor token generar                            # acceso al panel desde otros equipos
motor negocio poner finanzas.tasa_devolucion 25%
motor productos agregar --sku MI-PROD --nombre "Mi producto" --costo 20000 --flete 14000 --oferta x1:1:79900
motor ayuda                                    # lista completa
```

**Seguridad:** mientras no exista un token, el panel solo abre desde el mismo computador. Los secretos nunca
vuelven al navegador (se muestran enmascarados) y, desde la terminal, se leen por entrada estandar (`--clave -`)
para que no queden en el historial.

Guia paso a paso (WhatsApp, Bre-B, Dropi): [`docs/CONFIGURACION.md`](docs/CONFIGURACION.md)
Propuesta completa: [`docs/PROPUESTA.md`](docs/PROPUESTA.md)
Por que Arxi todavia no es el motor de agentes: [`docs/DECISION-ARXI.md`](docs/DECISION-ARXI.md)

## Estructura

```
motor/
├── cmd/motor/            # programa principal y terminal (cli.go)
├── internal/
│   ├── agent/            # vendedora IA: loop de herramientas sobre la API compatible con OpenAI
│   ├── llm/              # cliente OpenAI-compatible, presets, listar modelos, errores en espanol
│   ├── settings/         # ajustes en base de datos, validacion, vistas enmascaradas
│   ├── hub/              # integraciones vivas (IA, WhatsApp, Wompi) que se reconstruyen al cambiar ajustes
│   ├── catalog/          # catalogo multiproducto en base de datos
│   ├── sales/            # ciclo de vida del pedido (la IA nunca decide dinero)
│   ├── rules/            # zonas/anticipos y rentabilidad con devoluciones (con pruebas)
│   ├── convo/            # orquestador: agrupar mensajes, Yo tomo, comprobantes, eventos en vivo
│   ├── whatsapp/         # WhatsApp Cloud API oficial + verificación de firma
│   ├── payments/         # Wompi: links de pago + verificación de eventos
│   ├── store/            # SQLite (sin CGO, compila igual en Windows)
│   └── api/              # API HTTP + panel web embebido
├── knowledge/            # semilla inicial: playbook, ficha de LullaBites, reglas de zona
└── negocio.yaml          # semilla inicial de ajustes (despues manda la base de datos)
```

## Pruebas

```bash
cd motor && go test ./...
```
Cubren las reglas de zona y rentabilidad, la firma de Wompi y WhatsApp, el cliente de IA y sus presets, los ajustes y
secretos enmascarados, el catalogo multiproducto, el acceso local sin token y el flujo de venta
completo (contraentrega en Medellín, anticipo en zona lejana, zona roja, Yo tomo, escalamiento, mensajes agrupados
y cambios después de confirmar).
