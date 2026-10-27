# Decisión: cómo se usa Arxi en la Fase 1

**Estado:** decidido para la Fase 1 · revisar en la Fase 4 (Cazador + Propuestas).

## Contexto

La propuesta recomendaba usar el núcleo de Arxi (`github.com/michiTrader/arxi`, API `host/v1`)
como motor de agentes. Al implementar la Fase 1 revisé el contrato real de `host/v1`
(commit `633d6d1`, oct-2026):

- `TextProvider.CompleteText(TextRequest) → TextResponse{Text}`. El propio código lo dice:
  *"native tool calls are outside this contract"*.
- El **loop nativo de herramientas** (Fase 2 del roadmap de Arxi: `internal/turn`, `internal/exec`)
  existe, pero vive en paquetes `internal/`, que Go no permite importar desde otro módulo.

El Vendedor **depende** de herramientas nativas: guardar datos del pedido, confirmar, consultar envío
y escalar. Sin ellas, la IA tendría que "escribir" pedidos en texto libre y la regla central se
rompería: **la IA conversa, las reglas deciden el dinero**.

## Decisión

Para la Fase 1, el Motor usa **su propio loop corto de herramientas** sobre la API compatible con OpenAI
(`motor/internal/llm` + `motor/internal/agent`, unas 400 líneas). Copia los principios de Arxi que
aplican a este caso:

| Principio de Arxi | Cómo quedó en el Motor |
|---|---|
| El kernel no hace efectos: decide y otro ejecuta | El agente solo **pide** herramientas; `internal/sales` valida y decide (zonas, anticipos, estados) |
| Aprobación humana (`ask`) | Pedidos `por_aprobar` + botón **Aceptar**; auto-aprobación solo con límites configurables |
| Techo de rondas de herramientas | `MaxToolRounds = 6` por respuesta |
| Auditoría (`why`) | Tabla `audit`: cada herramienta, con sus argumentos y su resultado → "🧠 Qué hizo la IA" |
| Fallar cerrado | Zona desconocida → anticipo, nunca contraentrega. Webhooks con firma |

## Cuándo integrar Arxi de verdad

Cuando `host/v1` publique herramientas nativas (o se exponga `turn`/`exec` como API pública), o en la
**Fase 4**. Ahí los agentes Cazador, Analista y Media Buyer sí encajan en *blueprints* con etapas,
presupuesto por árbol de agentes y *triggers* programados. El cambio queda acotado porque el
orquestador solo conoce la interfaz `convo.Responder`.

Propuesta concreta para el repo de Arxi: exponer en `host/v1` un `ToolTextProvider` con
`Complete(messages, tools) → (text | tool_calls)`. Con eso el Vendedor se puede mover a Arxi sin reescribirlo.
