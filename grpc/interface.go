package grpc

import (
	"context"
	"net"

	grpcgo "google.golang.org/grpc"
)

// Interface define el contrato común de un servidor gRPC reutilizable.
//
// El wrapper administra el listener y el apagado, pero expone el servidor
// oficial para registrar servicios generados por protoc sin duplicar su API.
type Interface interface {
	// Driver devuelve el servidor nativo de grpc-go.
	Driver() *grpcgo.Server

	// Address devuelve la dirección configurada o la dirección efectiva cuando
	// ya existe un listener (útil cuando se utiliza el puerto 0).
	Address() string

	// ListenAndServe crea el listener configurado y sirve solicitudes.
	ListenAndServe() error

	// Serve sirve solicitudes sobre un listener proporcionado por el llamador.
	Serve(listener net.Listener) error

	// Shutdown permite terminar los RPC activos hasta que venza el contexto; al
	// vencer, fuerza la detención mediante grpc.Server.Stop.
	Shutdown(ctx context.Context) error
}

// Valida en tiempo de compilación que Server implementa Interface.
var _ Interface = (*Server)(nil)
