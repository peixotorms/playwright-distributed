package models

type MetricsResponse struct {
	ActiveConnections int64 `json:"activeConnections"`
}

type MessageResponse struct {
	Message string `json:"message"`
}
