package proxy

import (
	"net/http"
	"net/url"
	"proxy/pkg/logger"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("Failed to upgrade client connection: %v", err)
		return
	}
	defer clientConn.Close()

	backendURL, _ := url.Parse("ws://localhost:3131/playwright/eda2b7f4-db29-492a-bed2-258455a8d30f")
	serverConn, _, err := websocket.DefaultDialer.Dial(backendURL.String(), nil)
	if err != nil {
		logger.Error("Failed to connect to browser server: %v", err)
		clientConn.WriteMessage(websocket.CloseMessage, []byte("Browser Server Error"))
		return
	}
	defer serverConn.Close()

	logger.Info("Proxy connection established")

	go relay(clientConn, serverConn, "client->server")
	go relay(serverConn, clientConn, "server->client")

	select {}
}

func relay(src, dst *websocket.Conn, direction string) {
	srcAddr := src.RemoteAddr()
	dstAddr := dst.RemoteAddr()

	for {
		msgType, message, err := src.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				logger.Error("Unexpected connection closed (%s): %v", direction, err)
			} else {
				logger.Info("Connection closed (%s) (%s)", direction, err)
			}
			return
		}

		err = dst.WriteMessage(msgType, message)
		if err != nil {
			logger.Error("Failed to relay message (%s): %v", direction, err)
			return
		}

		logger.Info("Relayed %s->%s: %d bytes", srcAddr, dstAddr, len(message))
	}
}

func StartProxyServer() {
	http.HandleFunc("/", proxyHandler)

	server := &http.Server{
		Addr:         ":8080",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logger.Info("Starting proxy server")
	server.ListenAndServe()
}
