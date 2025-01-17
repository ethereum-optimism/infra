package proxyd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/mux"
)

type AdminApiHandler struct {
	dynamicAuthenticator DynamicAuthenticator
	adminToken           string
}

type adminResponse struct {
	StatusCode int
	Details    string
}

type UnauthorizedReason string

func NewAdminApiHandler(
	dynamicAuthenticator DynamicAuthenticator, adminToken string,
) *AdminApiHandler {
	return &AdminApiHandler{
		dynamicAuthenticator: dynamicAuthenticator,
		adminToken:           adminToken,
	}
}

func writeAdminApiResponse(w http.ResponseWriter, response adminResponse) {
	responseString, err := json.MarshalIndent(response, "", "    ")
	if err != nil {
		log.Error("failed to marshal response struct into string", "error", err)

		response.StatusCode = http.StatusInternalServerError
		responseString = []byte("internal server error")
	}

	httpResponseCodesTotal.WithLabelValues(fmt.Sprintf("%d", response.StatusCode)).Inc()
	w.WriteHeader(response.StatusCode)
	if _, err := w.Write(responseString); err != nil {
		log.Error("failed to send response for admin rpc", "error", err)
	}
}

func (h *AdminApiHandler) isUserAuthorized(headerToken string) (bool, UnauthorizedReason) {
	if h.dynamicAuthenticator == nil {
		log.Warn("admin rpc endpoint called when dynamic authenticator disabled")
		return false, "admin rpc endpoint disabled"
	}

	if h.adminToken == "" {
		log.Warn("admin rpc endpoint called when dynamic authenticator disabled")
		return false, "missing admin token in the dynamic auth configuration"
	}

	if !strings.Contains(headerToken, h.adminToken) {
		log.Warn("admin rpc endpoint called when dynamic authenticator disabled")
		return false, "invalid token"
	}

	return true, ""
}

func (h *AdminApiHandler) handlePutMethod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	secret := vars["rpc-auth-key"]

	if len(secret) < 12 {
		log.Info(fmt.Sprintf("failed to add new secret(%s): too short", secret))
		writeAdminApiResponse(w, adminResponse{
			StatusCode: http.StatusBadRequest,
			Details:    "secret too short: expected at least 12 characters",
		})
		return
	}

	if err := h.dynamicAuthenticator.NewSecret(secret); err != nil {
		log.Error(fmt.Sprintf("failed to add new secret(%s)", secret), "error", err)
		writeAdminApiResponse(w, adminResponse{
			StatusCode: http.StatusInternalServerError,
			Details:    fmt.Sprintf("internal server error: %s", err.Error()),
		})
		return
	}

	log.Info(fmt.Sprintf("new secret added(%s)", secret))
	writeAdminApiResponse(w, adminResponse{
		StatusCode: http.StatusOK,
		Details:    "new token added",
	})
}

func (h *AdminApiHandler) handleDeleteMethod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	secret := vars["rpc-auth-key"]

	if err := h.dynamicAuthenticator.DeleteSecret(secret); err != nil {
		log.Error(fmt.Sprintf("failed to delete secret(%s)", secret), "error", err)
		writeAdminApiResponse(w, adminResponse{
			StatusCode: http.StatusInternalServerError,
			Details:    fmt.Sprintf("internal server error: %s", err.Error()),
		})
		return
	}

	log.Info(fmt.Sprintf("secret deleted(%s)", secret))
	writeAdminApiResponse(w, adminResponse{
		StatusCode: http.StatusOK,
		Details:    "token deleted",
	})
}

func (h *AdminApiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authToken := r.Header.Get("Authorization")

	if authorized, reason := h.isUserAuthorized(authToken); !authorized {
		writeAdminApiResponse(w, adminResponse{
			StatusCode: http.StatusUnauthorized,
			Details:    string(reason),
		})
		return
	}

	if r.Method == http.MethodPut {
		h.handlePutMethod(w, r)
		return
	} else if r.Method == http.MethodDelete {
		h.handleDeleteMethod(w, r)
		return
	}

	writeAdminApiResponse(w, adminResponse{
		StatusCode: http.StatusBadRequest,
		Details:    "invalid http method",
	})
}
