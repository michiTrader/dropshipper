package llm

import (
	"context"
	"errors"
	"net"
	"strings"
)

// Friendly turns a technical error into short Spanish advice for a
// non-technical owner.
func Friendly(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrNoAPIKey) {
		return "Falta la clave (API key) del proveedor. Pégala en el campo correspondiente."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "El proveedor tardó demasiado en responder. Revisa la dirección (URL) o inténtalo de nuevo."
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	var ne net.Error
	switch {
	case strings.Contains(low, "http 401") || strings.Contains(low, "http 403") || strings.Contains(low, "invalid api key") || strings.Contains(low, "unauthorized"):
		return "El proveedor rechazó la clave. Revisa que esté completa, sin espacios y que siga activa."
	case strings.Contains(low, "http 402") || strings.Contains(low, "insufficient") || strings.Contains(low, "balance") || strings.Contains(low, "quota"):
		return "La cuenta del proveedor no tiene saldo o cupo. Recarga saldo en su panel e inténtalo de nuevo."
	case strings.Contains(low, "http 404") && strings.Contains(low, "model"):
		return "El proveedor no reconoce ese modelo. Usa 'Cargar modelos' para elegir uno de la lista."
	case strings.Contains(low, "http 404"):
		return "La dirección no responde como un proveedor compatible con OpenAI. Revisa la URL (normalmente termina en /v1)."
	case strings.Contains(low, "http 400") && strings.Contains(low, "model"):
		return "El proveedor no acepta ese modelo. Usa 'Cargar modelos' para elegir uno válido."
	case strings.Contains(low, "http 429"):
		return "El proveedor está limitando las solicitudes (demasiadas seguidas). Espera un momento y reintenta."
	case strings.Contains(low, "http 5"):
		return "El proveedor tiene un problema temporal. Reintenta en unos minutos."
	case strings.Contains(low, "no such host") || strings.Contains(low, "connection refused") || errors.As(err, &ne):
		return "No se pudo conectar con esa dirección. Revisa la URL y tu conexión a internet."
	case strings.Contains(low, "respuesta de ia inválida"):
		return "La dirección respondió, pero no con el formato esperado. Verifica que sea la URL base de una API compatible con OpenAI."
	}
	return msg
}
