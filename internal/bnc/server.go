package bnc

import (
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/gliderlabs/ssh"
	"github.com/kofany/gNb/internal/types"
	"github.com/kofany/gNb/internal/util"
)

type BNCServer struct {
	Bot      types.Bot
	Port     int
	Password string
	Tunnel   *RawTunnel
}

func StartBNCServer(bot types.Bot) (*BNCServer, error) {
	util.Debug("Starting BNC server for bot %s", bot.GetCurrentNick())
	port := randomPort()
	password := generatePassword()
	server := &BNCServer{
		Bot:      bot,
		Port:     port,
		Password: password,
		Tunnel:   NewRawTunnel(bot),
	}
	util.Debug("BNC server created with port %d and password %s", port, password)
	go server.listen()
	return server, nil
}

func (s *BNCServer) listen() {
	util.Debug("BNC server listening started for bot %s", s.Bot.GetCurrentNick())

	// Konfiguracja serwera SSH
	server := &ssh.Server{
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			util.Debug("Password authentication attempt for user: %s", ctx.User())
			return ctx.User() == s.Bot.GetCurrentNick() && password == s.Password
		},
		Handler: func(sess ssh.Session) {
			util.Debug("New SSH connection received for bot %s", s.Bot.GetCurrentNick())
			util.Debug("User: %s, Command: %v", sess.User(), sess.Command())

			if sess.User() != s.Bot.GetCurrentNick() {
				util.Warning("BNC authentication failed: incorrect username for bot %s", s.Bot.GetCurrentNick())
				sess.Close()
				return
			}
			if len(sess.Command()) == 0 {
				util.Warning("BNC authentication failed: no command provided for bot %s", s.Bot.GetCurrentNick())
				sess.Close()
				return
			}
			if sess.Command()[0] != s.Password {
				util.Warning("BNC authentication failed: incorrect password for bot %s", s.Bot.GetCurrentNick())
				sess.Close()
				return
			}

			util.Info("BNC connection established for bot %s", s.Bot.GetCurrentNick())
			s.Tunnel.Start(sess)

			// Keep the session alive
			for {
				time.Sleep(time.Second * 30)
				if _, err := sess.SendRequest("keepalive", false, nil); err != nil {
					util.Debug("Keepalive failed for bot %s: %v", s.Bot.GetCurrentNick(), err)
					break
				}
			}
		},
	}

	// Use an empty host key
	server.SetOption(ssh.HostKeyFile(""))

	// Nasłuchuj na IPv4 i IPv6
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		util.Error("Failed to start BNC listener: %v", err)
		return
	}
	defer listener.Close()

	util.Info("Starting BNC server for bot %s on port %d", s.Bot.GetCurrentNick(), s.Port)

	err = server.Serve(listener)
	if err != nil {
		util.Error("Failed to serve BNC: %v", err)
	}
}

func (s *BNCServer) Stop() {
	util.Debug("Stopping BNC server for bot %s", s.Bot.GetCurrentNick())
	s.Tunnel.Stop()
}

func randomPort() int {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(1000) + 4000 // Random port between 50000 and 59999
}

func generatePassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, 16)
	for i := range password {
		password[i] = charset[rand.Intn(len(charset))]
	}
	return string(password)
}
