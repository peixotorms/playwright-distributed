package models

type ErrorDetails struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorDetails `json:"error"`
}
