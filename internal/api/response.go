package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Meta    interface{} `json:"meta,omitempty"`
	Error   *ErrorBody  `json:"error,omitempty"`
}

type PageMeta struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
}

func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Success: true, Data: data})
}

func Created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, Response{Success: true, Data: data})
}

func OKPage(c *gin.Context, data interface{}, total, page, size int) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
		Meta:    PageMeta{Total: total, Page: page, Size: size},
	})
}

func Error(c *gin.Context, status int, code, message string, details interface{}) {
	c.JSON(status, Response{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message, Details: details},
	})
}

func BadRequest(c *gin.Context, code, message string, details interface{}) {
	Error(c, http.StatusBadRequest, code, message, details)
}

func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "unauthorized", message, nil)
}

func Forbidden(c *gin.Context, code, message string, details interface{}) {
	Error(c, http.StatusForbidden, code, message, details)
}

func NotFound(c *gin.Context, code, message string) {
	Error(c, http.StatusNotFound, code, message, nil)
}

func Conflict(c *gin.Context, code, message string, details interface{}) {
	Error(c, http.StatusConflict, code, message, details)
}

func Internal(c *gin.Context, code, message string, details interface{}) {
	Error(c, http.StatusInternalServerError, code, message, details)
}
