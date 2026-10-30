# Guia de configuracion y uso (Fase 1: Vendedor IA por WhatsApp)

Esta guia te lleva de cero a tener la vendedora IA atendiendo tu WhatsApp y dejando los pedidos listos para que
tu los apruebes.

**Todo se puede hacer de dos maneras, y las dos editan los mismos datos:**

- **Panel web** (`http://localhost:8080`): formularios guiados con boton de **Probar conexion** y mensajes de error claros.
- **Terminal** (`motor ...`): los mismos ajustes como comandos. Cada bloque del panel trae un desplegable
  **"Hacerlo desde la terminal"** con el comando equivalente.

Los datos viven en la base de datos del Motor (`data/`). Si el Motor esta encendido y cambias algo desde la
terminal, lo ve en pocos segundos, sin reiniciar. El archivo `.env` es **opcional**: solo siembra valores en el
primer arranque (util para servidores sin nadie que abra el panel).

---

## 1. Instalar y arrancar el Motor

1. Instala **Go 1.26 o superior**: <https://go.dev/dl/>.
2. Descarga el repo:
   ```bash
   git clone https://github.com/michiTrader/dropshipper.git
   cd dropshipper/motor
   ```
3. Compila (o usa `go run ./cmd/motor` en su lugar):
   ```bash
   go build -o motor ./cmd/motor        # en Windows: motor.exe
   ```
4. Enciende el Motor:
   ```bash
   ./motor
   ```
5. Abre **http://localhost:8080**. Veras **Inicio** con una lista de **puesta en marcha** que te dice que falta.

Para ver el mismo resumen desde la terminal:

```bash
./motor estado
```

> **Seguridad por defecto.** Mientras no exista un token de acceso, el panel solo abre desde el mismo computador
> donde corre el Motor. Cualquier otro equipo recibe "acceso restringido". Cuando quieras entrar desde el celular o
> publicar el Motor en internet, crea un token (seccion 6).

---

## 2. Conectar la inteligencia artificial

Sirve **cualquier proveedor compatible con la API de OpenAI**. Solo necesitas tres datos: la direccion de la API,
el modelo y la clave.

**En el panel:** Conexiones > Inteligencia artificial > **Agregar proveedor**.

1. Elige un punto de partida (DeepSeek, OpenAI, OpenRouter, Groq, Gemini, Ollama local o Personalizado).
   Para **Arxi** u otro servicio propio elige *Personalizado* y escribe su URL.
2. Pega la clave de API.
3. Pulsa **Cargar modelos** para elegir el modelo de la lista que ofrece el proveedor (o escribelo).
4. Pulsa **Probar conexion**: hace una llamada real y te dice si funciona o por que no
   (clave incorrecta, saldo agotado, modelo inexistente, URL equivocada, servidor caido...).
5. **Guardar y usar**.

Puedes tener varios proveedores guardados y cambiar el que esta en uso con un clic.

**Desde la terminal:**

```bash
motor ia presets                                             # proveedores conocidos
echo $MI_CLAVE | motor ia agregar --preset deepseek --clave - --activar
motor ia agregar --nombre Arxi --url https://tu-arxi/v1 --modelo mi-modelo --clave - --activar   # la URL suele terminar en /v1
motor ia modelos                                             # modelos disponibles
motor ia probar                                              # llamada real de prueba
motor ia listar | activar ID | quitar ID
```

**Sobre la URL:** debe ser la direccion base de una API compatible con OpenAI, normalmente terminada en `/v1` (por ejemplo `https://api.ejemplo.com/v1`). Si pegas solo el dominio (`https://api.ejemplo.com`) y el proveedor responde "Route ... not found" (404), el Motor reintenta solo con `/v1`, recuerda la que funciona y, al probar la conexion, te sugiere guardar la direccion corregida. No incluyas `/chat/completions`.

Si la IA falla al responder, el Motor crea una alerta con la causa en espanol (clave rechazada, sin saldo, URL incorrecta, modelo inexistente...) y deja el chat para que respondas tu.

El guion en `--clave -` lee la clave de la entrada estandar, para que no quede en el historial de la terminal.
La clave nunca se vuelve a mostrar completa: el panel solo muestra los ultimos 4 caracteres, y al editar un
proveedor, dejar el campo vacio conserva la clave guardada.

---

## 3. Productos

El Motor vende **varios productos**. En **Productos** ves la lista; al abrir uno tienes cuatro pestanas:

| Pestana | Que contiene |
|---|---|
| **Ficha** | Nombre, descripcion, modo de uso, ingredientes, precauciones, envio y garantia. La IA no inventa nada que no este aqui |
| **Ofertas y costos** | Costo del producto, flete, ofertas (1, 2, 3 unidades...) y la casilla **Costo confirmado** |
| **Finanzas** | Ganancia por oferta (si entrega, si devuelve, esperada), CPA maximo para anuncios y los resultados reales de ese producto. Se recalcula mientras escribes |
| **Argumentos de venta** | Dolores del cliente, beneficios y como responder a cada objecion |

Un producto nuevo se crea **inactivo**; pulsa **Activar para vender** cuando este listo. Mientras el costo no este
confirmado con el proveedor, la rentabilidad se marca como estimada.

Un producto con pedidos no se puede borrar (desactivalo), y siempre debe quedar al menos un producto activo.

La **Guia de ventas** (boton en la lista) son las instrucciones generales de la IA para todos los productos.

**Desde la terminal:**

```bash
motor productos listar
motor productos agregar --sku CREMA-50 --nombre "Crema 50ml" --costo 20000 --flete 14000 \
      --oferta x1:1:79900 --oferta x2:2:129900
motor productos editar CREMA-50 --costo 21000 --costo-confirmado=true
motor productos exportar CREMA-50 > crema.yaml      # editar la ficha completa en un archivo
motor productos importar crema.yaml
motor productos activar CREMA-50
motor guia ver > guia.md  &&  motor guia poner guia.md
```

---

## 4. Datos para cobrar anticipos

En zonas de riesgo el cliente paga un anticipo antes del despacho.

**En el panel:** Conexiones > **Cobro de anticipos**. Elige el metodo:

- **Manual**: pones tu llave Bre-B y/o los datos de tu cuenta. El sistema (no la IA) envia al cliente el valor
  exacto y una referencia. El cliente manda el comprobante, te llega una alerta, **verificas en tu banco** y
  pulsas **Pago recibido** en Aprobaciones. La IA nunca da un pago por recibido solo por una captura.
- **Wompi** (opcional): confirmacion automatica, sin revisar capturas. Abre una cuenta en
  <https://comercios.wompi.co>, copia tus llaves en el bloque **Wompi** y pulsa **Probar conexion**.
  En Wompi, registra la **URL de eventos** que muestra el panel. Wompi cobra comision por transaccion.

**Desde la terminal:**

```bash
motor negocio poner pagos.metodo manual
motor negocio poner pagos.manual.llave_bre_b 3001234567
motor negocio poner pagos.manual.banco Bancolombia

echo $WOMPI_PRIVADA | motor wompi configurar --llave-publica pub_test_xxx --llave-privada - --eventos SECRETO --pruebas
motor wompi probar
motor negocio poner pagos.metodo wompi
```

---

## 5. Conectar WhatsApp (API oficial de Meta)

> Usa un numero **dedicado**. Ese numero **no puede** estar al mismo tiempo en la app normal de WhatsApp o
> WhatsApp Business. Si ya lo tienes instalado ahi, primero borra la cuenta de esa app.

### 5.1 Direccion publica (para recibir mensajes)

Meta necesita una URL **https** publica. Desde tu PC, lo mas simple es Cloudflare Tunnel:

```bash
cloudflared tunnel --url http://localhost:8080
```

Te da una URL como `https://algo.trycloudflare.com` (cambia cada vez que reinicias el tunel; para algo estable usa
un tunel con nombre o un servidor). Guardala en **Conexiones > Seguridad y acceso > Direccion publica**, o:

```bash
motor url https://algo.trycloudflare.com
```

Con ella, el panel te muestra las direcciones exactas de los webhooks para copiar:
`.../webhook/whatsapp` y `.../webhook/wompi`.

### 5.2 Crear la app en Meta

1. <https://developers.facebook.com/apps> > **Crear app** > tipo **Empresa (Business)**.
2. Agrega el producto **WhatsApp** y vincula tu cuenta de Meta Business.
3. En **WhatsApp > Configuracion de la API**: agrega tu numero dedicado, verificalo y copia el **ID del numero de telefono**.
4. Crea un **token permanente**: Meta Business Suite > Configuracion del negocio > **Usuarios del sistema** > crea un
   administrador > **Generar token** con los permisos `whatsapp_business_messaging` y `whatsapp_business_management`.
   (El token temporal de la consola dura 24 h y no sirve para produccion.)
5. En **Configuracion de la app > Basica** copia el **Secreto de la app**.

### 5.3 Pegar los datos en el Motor

**En el panel:** Conexiones > **WhatsApp Business**. Completa el token, el ID del numero y el secreto de la app.
En **Token de verificacion** pulsa **Generar** (o inventa uno). Pulsa **Probar conexion** y luego **Guardar**.

**Desde la terminal:**

```bash
echo $WA_TOKEN | motor whatsapp configurar --token - --telefono-id 123456789 --verify-token mi-texto-secreto
echo $WA_SECRET | motor whatsapp configurar --app-secret -
motor whatsapp probar
```

### 5.4 Registrar el webhook en Meta

En **WhatsApp > Configuracion > Webhook**:
- **URL de devolucion de llamada:** la que muestra el panel (`https://TU_DIRECCION/webhook/whatsapp`).
- **Token de verificacion:** el mismo que guardaste en el Motor.
- Pulsa **Verificar y guardar** y suscribete al campo **messages**.

Escribe desde otro celular a tu numero dedicado: la vendedora debe responder y el chat aparece en **Conversaciones**.
No necesitas reiniciar el Motor en ningun paso.

**Verificacion del negocio:** para superar los limites iniciales de mensajes, Meta pide verificar tu negocio
(documentos) en Meta Business Suite. Las respuestas a clientes que te escribieron en las ultimas 24 h no requieren plantillas.

---

## 6. Seguridad y acceso desde otros equipos

**En el panel:** Conexiones > **Seguridad y acceso** > **Generar token de acceso**. El token se muestra una sola vez:
guardalo. Este navegador ya queda con la sesion iniciada; en otros dispositivos pega el token en la pantalla de acceso.

**Desde la terminal:**

```bash
motor token generar        # crea un token y lo muestra una sola vez
motor token poner MI_TOKEN_LARGO    # minimo 8 caracteres
motor token quitar         # el panel vuelve a abrir solo desde este computador
```

Antes de publicar el Motor en internet, **define siempre un token**.

---

## 7. Tu dia a dia

| Pantalla | Para que |
|---|---|
| **Inicio** | Ventas, pendientes, alertas y la lista de puesta en marcha |
| **Aprobaciones** | **Aprobar** pedidos, **confirmar pagos**, **copiar datos para Dropi** y **registrar la guia** |
| **Conversaciones** | Ver los chats en vivo. **Tomar el chat** pausa la IA; **Que hizo la IA** muestra cada decision |
| **Pedidos** | Historial y cambio de estado (en transito, entregado, novedad, devuelto) |
| **Simulador** | Probar la vendedora sin usar WhatsApp |
| **Productos** | Lista de productos; ficha, ofertas y finanzas de cada uno; consulta de zonas de envio |
| **Conexiones** | IA, WhatsApp, cobros y seguridad, con pruebas de conexion |
| **Ajustes** | Persona de la IA, autonomia, supuestos financieros, Dropi y avisos |

**Flujo de un pedido (Dropi todavia es manual):**
1. La vendedora conversa, toma los datos y el cliente confirma el resumen.
2. Te llega **Pedido por aprobar**: revisas y pulsas **Aprobar**. El cliente recibe la confirmacion.
3. **Copiar datos para Dropi**: creas la orden en Dropi y pegas los datos.
4. **Registrar guia**: el cliente recibe la guia y el valor a pagar al recibir.

**Cuando la IA te necesita:** si el cliente pide hablar con una persona, quiere una llamada, tiene un reclamo o hay un
tema de salud delicado, la IA **se pausa sola**, avisa al cliente y te deja una alerta. Si escribes en un chat desde el
panel, la IA tambien se pausa en ese chat; para devolverlo usa **Devolver a la IA**.

### Ajustes del negocio

En **Ajustes** (o con `motor negocio poner CLAVE VALOR`) cambias, sin reiniciar:

- **Persona**: nombre, rol y tono de la vendedora. `motor negocio poner persona.nombre Valeria`
- **Autonomia**: si la IA aprueba sola los pedidos de bajo riesgo y con que limites. Por defecto, tu apruebas todo.
- **Supuestos financieros** (en %): devoluciones esperadas, pedidos que se confirman y margen de seguridad.
  `motor negocio poner finanzas.tasa_devolucion 25%`
- **Conversacion**: tiempo para agrupar mensajes, pausa entre mensajes y memoria.
- **Dropi y avisos**: modo manual o por API, y tu WhatsApp para recibir avisos.

---

## 8. Reglas de zona (contraentrega vs. anticipo)

Archivo: `motor/knowledge/zonas.yaml`. Prioridad: **barrio > municipio > departamento > por defecto**.

| Nivel | Pago por defecto |
|---|---|
| `normal` | Contraentrega |
| `lejano` | Anticipo del 20 % (minimo $20.000) |
| `dificil_acceso` | Anticipo del 50 % (minimo $30.000) |
| `zona_roja` | 100 % anticipado |

Un departamento que no aparece **nunca** queda en contraentrega: se usa `por_defecto: lejano`. Agrega barrios
problematicos con tu experiencia real, por ejemplo `"medellin|nombre del barrio": zona_roja`. Despues de editar,
reinicia el Motor. Para comprobar que haria el Motor con un destino, usa **Productos > Consultar zona de envio**.

---

## 9. Lo que falta conectar (siguientes fases)

- **Dropi por API**: solicita a Dropi el **token de integraciones** y la documentacion. Con eso, **Aprobar** creara la
  orden sola y las guias se actualizaran automaticamente.
- **DropKiller (MCP)**: requiere el plan **Advanced**.
- **Meta Ads, landings y Cazador de productos**: fases 3 a 6 de la propuesta.

## Dropi y transportadoras

**Conectar Dropi** (Conexiones por API o terminal; ambas editan los mismos datos):

```
echo $TOKEN_DROPI | motor dropi configurar --token - --ciudad-origen Medellin --produccion
motor dropi probar
```

- El token se crea en Dropi > Configuración > Integraciones.
- El entorno de pruebas (`--pruebas`) NO tiene transportadoras: sirve para validar el token, no para cotizar fletes.
- Cada producto necesita su "ID de Dropi" para traer el costo real (`motor dropi costos SKU`), que lo marca como confirmado.

**"Costo sin confirmar"**: el producto tiene un costo estimado. Se confirma cuando lo marcas como confirmado o cuando usas `motor dropi costos SKU`. Conectar Dropi por sí solo no lo confirma.

**Transportadoras** (Veloces, Envía, Interrapidísimo, Domina, Coordinadora, 99minutos, TCC, Wiilog, Jamv-drive, Derocha-express):

```
motor transportadoras ver
motor transportadoras notas veloces "Buena en Medellin; no recibe contraentrega"
motor transportadoras tasa envia 18%
motor transportadoras desactivar tcc
```

Las notas se inyectan en el prompt de la IA. El Motor compara las activas con la fórmula de ganancia (ganancia si entrega, pérdida si devuelve, ganancia esperada) y elige la de mayor ganancia esperada.

**Simular un pedido sin crear nada en Dropi**:

```
motor dropi cotizar  --ciudad Bogota --producto SKU
motor dropi simular  --ciudad Bogota --producto SKU
```

Verifica conexión, flete cotizado frente al estimado, costo frente al de Dropi, costo confirmado, rentabilidad y código DANE, y muestra el pedido que se enviaría (no se envía).

**Datos del cliente**: la IA pide correo electrónico, no cédula.
