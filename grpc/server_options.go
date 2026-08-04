package grpc

import (
	"fmt"
	"net"

	grpcgo "google.golang.org/grpc"
)

// ServerOption configura un Server durante su construcción.
type ServerOption func(*Server)

// WithGRPCOptions agrega opciones del servidor oficial. Se aplican en el mismo
// orden en que se proporcionan.
func WithGRPCOptions(options ...grpcgo.ServerOption) ServerOption {
	copiedOptions := append([]grpcgo.ServerOption(nil), options...)
	return func(server *Server) {
		server.driverOptions = append(server.driverOptions, copiedOptions...)
	}
}

// WithUnaryInterceptors agrega una cadena ordenada de interceptores unary.
func WithUnaryInterceptors(interceptors ...grpcgo.UnaryServerInterceptor) ServerOption {
	copiedInterceptors := append([]grpcgo.UnaryServerInterceptor(nil), interceptors...)
	return func(server *Server) {
		for index, interceptor := range copiedInterceptors {
			if interceptor == nil {
				server.configurationErrors = append(
					server.configurationErrors,
					fmt.Errorf("interceptor unary en posicion %d no puede ser nil", index),
				)
			}
		}
		server.driverOptions = append(
			server.driverOptions,
			grpcgo.ChainUnaryInterceptor(copiedInterceptors...),
		)
	}
}

// WithStreamInterceptors agrega una cadena ordenada de interceptores streaming.
func WithStreamInterceptors(interceptors ...grpcgo.StreamServerInterceptor) ServerOption {
	copiedInterceptors := append([]grpcgo.StreamServerInterceptor(nil), interceptors...)
	return func(server *Server) {
		for index, interceptor := range copiedInterceptors {
			if interceptor == nil {
				server.configurationErrors = append(
					server.configurationErrors,
					fmt.Errorf("interceptor streaming en posicion %d no puede ser nil", index),
				)
			}
		}
		server.driverOptions = append(
			server.driverOptions,
			grpcgo.ChainStreamInterceptor(copiedInterceptors...),
		)
	}
}

// WithNetwork establece la red utilizada por ListenAndServe. El valor
// predeterminado es tcp.
func WithNetwork(network string) ServerOption {
	return func(server *Server) {
		server.network = network
	}
}

// WithListenConfig establece la configuración utilizada para crear listeners.
// El valor se copia y puede reutilizarse fuera del servidor.
func WithListenConfig(configuration net.ListenConfig) ServerOption {
	return func(server *Server) {
		server.listenConfig = configuration
	}
}
