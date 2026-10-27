# Guía de configuración y uso (Fase 1: Vendedor IA por WhatsApp)

Esta guía te lleva de cero a tener a **Sofi** (la vendedora IA) atendiendo tu WhatsApp
y dejando los pedidos listos para que tú los apruebes con un botón.

---

## 1. Instalar y arrancar el Motor (5 minutos)

1. Instala **Go 1.26 o superior**: <https://go.dev/dl/>.
2. Descarga el repo y entra a la carpeta del Motor:
   ```bash
   git clone https://github.com/michiTrader/dropshipper.git
   cd dropshipper/motor
   ```
3. Crea tu archivo de secretos:
   ```bash
   cp .env.example .env
   ```
   Abre `.env` y completa por lo menos:
   - `LLM_API_KEY=` → tu clave de **DeepSeek** (<https://platform.deepseek.com> → API keys).
   - `ADMIN_TOKEN=` → una contraseña larga que inventas para entrar al panel.
4. Arranca:
   ```bash
   go run ./cmd/motor
   ```
5. Abre **http://localhost:8080**, entra con tu `ADMIN_TOKEN` y ve a **🧪 Simulador**.
   Ahí puedes hablar con Sofi como si fueras un cliente, **sin tocar WhatsApp**.

> Para dejarlo como programa: `go build -o motor ./cmd/motor` (en Windows: `motor.exe`).

---

## 2. Datos para cobrar anticipos (Bre-B / Bancolombia)

Edita `motor/negocio.yaml`:

```yaml
pagos:
  metodo: manual
  manual:
    llave_bre_b: "TU_LLAVE"        # celular, cédula, correo o @llave registrada en Bancolombia
    banco: "Bancolombia"
    tipo_cuenta: "Ahorros"
    numero_cuenta: "XXX-XXXXXX-XX"
    titular: "Tu nombre"
```

Cómo funciona:
1. Si el cliente vive en una zona que requiere anticipo, la IA confirma el pedido y el
   **sistema** (no la IA) le envía tus datos de pago con el valor exacto y una referencia.
2. El cliente envía la captura del comprobante por WhatsApp.
3. Te llega una alerta. **Revisas tu app de Bancolombia** y presionas **💰 Pago recibido**.
4. El pedido pasa a **Por aprobar**.

La IA **nunca** da un pago por recibido solo por una captura, porque las capturas falsas son comunes.

**Opcional: Wompi.** Si quieres confirmación automática (sin revisar capturas), abre una cuenta
en <https://comercios.wompi.co>, pon `metodo: wompi` y completa `WOMPI_PRIVATE_KEY` y
`WOMPI_EVENTS_SECRET` en `.env`. Wompi cobra comisión por transacción.

---

## 3. Confirmar el producto en Dropi

Abre `motor/knowledge/productos/lullabites.yaml` y verifica en Dropi:
- `costo_proveedor` (precio de proveedor) y `flete_estimado`,
- precios de las ofertas (1, 2 y 3 frascos: $89.900 / $149.900 / $199.900 son **propuestos**, cámbialos si quieres),
- luego cambia `costo_confirmado: true`.

Mientras esté en `false`, cada pedido muestra "cifras estimadas".

---

## 4. Conectar WhatsApp (API oficial de Meta)

> Usa tu número **dedicado**. Ese número **no puede** estar al mismo tiempo en la app normal
> de WhatsApp o WhatsApp Business. Si lo tienes instalado ahí, primero borra la cuenta de esa app.

### 4.1 Exponer el Motor a internet (para recibir mensajes)

Meta necesita una URL **https** pública. La forma gratis desde tu PC es Cloudflare Tunnel:

```bash
# instala cloudflared: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/
cloudflared tunnel --url http://localhost:8080
```

Te da una URL tipo `https://algo.trycloudflare.com`. Ponla en `.env` como `PUBLIC_URL=`.
(Esa URL cambia cada vez que reinicias el túnel. Para algo estable usa un túnel con nombre o un servidor/VPS).

### 4.2 Crear la app en Meta

1. Entra a <https://developers.facebook.com/apps> → **Crear app** → tipo **Empresa (Business)**.
2. Agrega el producto **WhatsApp** y vincula tu **cuenta de Meta Business**.
3. En **WhatsApp → Configuración de la API**:
   - agrega tu número dedicado y verifícalo con el código SMS o la llamada,
   - copia el **Phone number ID** → `WHATSAPP_PHONE_NUMBER_ID=`.
4. Crea un **token permanente**: Meta Business Suite → Configuración del negocio → **Usuarios del sistema**
   → crea un usuario administrador → **Generar token** para tu app, con los permisos
   `whatsapp_business_messaging` y `whatsapp_business_management` → `WHATSAPP_TOKEN=`.
   (El token temporal de la consola dura 24 h y no sirve para producción).
5. En **Configuración de la app → Básica** copia el **App Secret** → `WHATSAPP_APP_SECRET=`.
6. Inventa una palabra secreta → `WHATSAPP_VERIFY_TOKEN=`.
7. **Reinicia el Motor**.
8. En **WhatsApp → Configuración → Webhook**:
   - URL de devolución de llamada: `https://TU_PUBLIC_URL/webhook/whatsapp`
   - Token de verificación: el mismo `WHATSAPP_VERIFY_TOKEN`
   - Presiona **Verificar y guardar** y suscríbete al campo **messages**.
9. Escribe desde otro celular a tu número dedicado: Sofi debe responder y el chat aparece en **💬 Conversaciones**.

**Verificación del negocio:** para pasar de los límites iniciales de mensajes, Meta pide verificar
tu negocio (documentos) en Meta Business Suite. Las respuestas a clientes que te escribieron en las
últimas 24 h no requieren plantillas.

---

## 5. Tu día a día

| Pantalla | Para qué |
|---|---|
| 🏠 **Inicio** | Ventas, pendientes y alertas |
| ✅ **Aprobaciones** | **Aceptar** pedidos, **confirmar pagos**, **copiar datos para Dropi** y **registrar la guía** |
| 💬 **Conversaciones** | Ver todos los chats en vivo. **✋ Yo tomo** pausa la IA en ese chat. **🧠 Qué hizo la IA** muestra cada decisión |
| 📦 **Pedidos** | Historial y cambio de estado (en tránsito, entregado, novedad, devuelto) |
| 🧪 **Simulador** | Probar a Sofi sin gastar WhatsApp |
| 💰 **Producto y finanzas** | Ganancia esperada con devoluciones, CPA máximo para anuncios y consulta de zonas |

**Flujo de un pedido (Dropi todavía es manual):**
1. Sofi vende, toma los datos y el cliente confirma el resumen.
2. Te llega **Pedido por aprobar** → revisas y presionas **✅ Aceptar**. El cliente recibe la confirmación.
3. **📋 Copiar datos para Dropi** → creas la orden en Dropi y la pegas.
4. **🚚 Registrar guía** → el cliente recibe la guía y el valor a pagar al recibir.

**Cuando Sofi te necesita:** si el cliente pide hablar con una persona, quiere una llamada, tiene un
reclamo o hay un tema de salud delicado, Sofi **se pausa sola**, le avisa al cliente y te deja una alerta.
Si escribes en un chat desde el panel, la IA se pausa automáticamente en ese chat. Para devolverlo, usa **🤖 Devolver a la IA**.

---

## 6. Reglas de zona (contraentrega vs. anticipo)

Archivo: `motor/knowledge/zonas.yaml`. Prioridad: **barrio > municipio > departamento > por defecto**.

| Nivel | Pago por defecto |
|---|---|
| `normal` | Contraentrega |
| `lejano` | Anticipo del 20 % (mínimo $20.000) |
| `dificil_acceso` | Anticipo del 50 % (mínimo $30.000) |
| `zona_roja` | 100 % anticipado |

Un departamento que no aparece **nunca** queda en contraentrega: se usa `por_defecto: lejano`.
Agrega barrios problemáticos con tu experiencia real, por ejemplo `"medellin|nombre del barrio": zona_roja`.
Después de editar, reinicia el Motor.

---

## 7. Lo que falta conectar (siguientes fases)

- **Dropi por API**: solicita a Dropi el **token de integraciones** y la documentación. Con eso, el botón
  Aceptar creará la orden sola y las guías se actualizarán automáticamente.
- **DropKiller (MCP)**: requiere el plan **Advanced**.
- **Meta Ads, landings y Cazador de productos**: fases 3 a 6 de la propuesta.
