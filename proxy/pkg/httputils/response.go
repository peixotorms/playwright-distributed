package httputils

import (
	"encoding/json"
	"net/http"
	"proxy/internal/models"
)

func ErrorResponse(w http.ResponseWriter, code int, message string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Error: models.ErrorDetails{
			Code:    code,
			Message: message,
		},
	})
}

func JSONResponse(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			ErrorResponse(w, http.StatusInternalServerError, "internal server error")
		}
	}
}
