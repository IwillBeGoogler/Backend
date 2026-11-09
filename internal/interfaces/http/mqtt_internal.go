package http

// The MQTT broker's two questions, asked over HTTP by EMQX (its HTTP
// authentication and HTTP authorization sources):
//
//	POST /api/internal/mqtt/auth   {clientid, username, password}      -> {result, is_superuser}
//	POST /api/internal/mqtt/acl    {clientid, username, action, topic} -> {result}
//
// They are not part of the public API: the broker sends the shared
// MQTT_INTERNAL_SECRET as "Authorization: Bearer ..." (without it, or with a
// wrong one, the answer is 401), the proxy in front of the API must not route
// /api/internal/ (docs/MQTT.md has the nginx rule), and they are not
// registered at all while MQTT_INTERNAL_SECRET is empty.

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func (h *Handler) mqttRoutes(mux *http.ServeMux) {
	if h.MQTT == nil || h.MQTTInternalSecret == "" {
		return
	}
	mux.HandleFunc("POST /api/internal/mqtt/auth", h.mqttAuth)
	mux.HandleFunc("POST /api/internal/mqtt/acl", h.mqttACL)
}

// mqttBroker reports whether the request carries the internal secret.
func (h *Handler) mqttBroker(w http.ResponseWriter, r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(h.MQTTInternalSecret)) != 1 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}

// mqttDecisions counts the broker's questions and the answers (kind: auth, publish, subscribe ...).
var mqttDecisions = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iwillbegoogler_mqtt_decisions_total",
	Help: "Broker auth and ACL decisions by kind and result.",
}, []string{"kind", "result"})

func mqttKind(action string) string {
	switch action {
	case "publish", "subscribe":
		return action
	}
	return "other"
}

type mqttAuthIn struct {
	ClientID string `json:"clientid"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type mqttAuthOut struct {
	Result      string `json:"result"`
	IsSuperuser bool   `json:"is_superuser"`
}

// mqttAuth decides a broker connection (HTTP authentication source).
//
// Auth: internal secret.
func (h *Handler) mqttAuth(w http.ResponseWriter, r *http.Request) {
	var in mqttAuthIn
	if !h.mqttBroker(w, r) || !decode(w, r, &in) {
		return
	}
	res := h.MQTT.Authenticate(r.Context(), in.ClientID, in.Username, in.Password)
	out := mqttAuthOut{Result: "deny"}
	if res.Allow {
		out = mqttAuthOut{Result: "allow", IsSuperuser: res.Superuser}
	}
	mqttDecisions.WithLabelValues("auth", out.Result).Inc()
	dgWrite(w, http.StatusOK, out)
}

type mqttACLIn struct {
	ClientID string `json:"clientid"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Topic    string `json:"topic"`
}

// mqttACL decides a publish or subscribe (HTTP authorization source).
//
// Auth: internal secret.
func (h *Handler) mqttACL(w http.ResponseWriter, r *http.Request) {
	var in mqttACLIn
	if !h.mqttBroker(w, r) || !decode(w, r, &in) {
		return
	}
	result := "deny"
	if h.MQTT.Authorize(r.Context(), in.ClientID, in.Username, in.Action, in.Topic) {
		result = "allow"
	}
	mqttDecisions.WithLabelValues(mqttKind(in.Action), result).Inc()
	dgWrite(w, http.StatusOK, map[string]string{"result": result})
}
