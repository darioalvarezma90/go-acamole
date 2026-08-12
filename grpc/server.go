package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	grpcgo "google.golang.org/grpc"
)

const defaultNetwork = "tcp"

var (
	// ErrNilContext indica que se recibió un contexto nil.
	ErrNilContext = errors.New("contexto no puede ser nil")

	// ErrNilListener indica que Serve recibió un listener nil.
	ErrNilListener = errors.New("listener no puede ser nil")

	// ErrServerClosed indica que se intentó iniciar un servidor cerrado.
	ErrServerClosed = errors.New("servidor grpc cerrado")

	// ErrServerRunning indica que la instancia ya está sirviendo solicitudes.
	ErrServerRunning = errors.New("servidor grpc en ejecucion")

	// ErrServerAlreadyServed indica que la instancia ya completó una ejecución.
	ErrServerAlreadyServed = errors.New("servidor grpc ya fue ejecutado")

	// ErrServerUnavailable indica que el wrapper no contiene un driver válido.
	ErrServerUnavailable = errors.New("servidor grpc no disponible")
)

// Server administra un grpc.Server y su listener. Los servicios deben
// registrarse mediante Driver antes de iniciar Serve o ListenAndServe.
type Server struct {
	address             string
	network             string
	listenConfig        net.ListenConfig
	driverOptions       []grpcgo.ServerOption
	configurationErrors []error
	driver              *grpcgo.Server

	stateMutex sync.RWMutex
	listener   net.Listener
	running    bool
	served     bool

	closed        atomic.Bool
	shutdownOnce  sync.Once
	forceStopOnce sync.Once
	doneOnce      sync.Once
	shutdownDone  chan struct{}
}

// NewServer construye un servidor gRPC. No abre sockets hasta invocar
// ListenAndServe; esto permite registrar primero los servicios generados.
func NewServer(address string, opts ...ServerOption) (*Server, error) {
	server := &Server{
		address:      address,
		network:      defaultNetwork,
		shutdownDone: make(chan struct{}),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(server)
		}
	}

	if err := server.validate(); err != nil {
		return nil, fmt.Errorf("error de configuracion del servidor grpc: %w", err)
	}
	driver, err := newGRPCServer(server.driverOptions)
	if err != nil {
		return nil, err
	}
	server.driver = driver
	return server, nil
}

// validate comprueba que la configuración interna del servidor sea válida.
func (s *Server) validate() error {
	if s == nil {
		return fmt.Errorf("servidor no puede ser nil")
	}
	if strings.TrimSpace(s.address) == "" {
		return fmt.Errorf("direccion no puede estar vacía")
	}
	if strings.TrimSpace(s.address) != s.address {
		return fmt.Errorf("direccion no puede contener espacios al inicio o al final")
	}
	if strings.TrimSpace(s.network) == "" {
		return fmt.Errorf("red no puede estar vacía")
	}
	if strings.TrimSpace(s.network) != s.network {
		return fmt.Errorf("red no puede contener espacios al inicio o al final")
	}
	for index, option := range s.driverOptions {
		if isNilInterface(option) {
			return fmt.Errorf("opcion grpc en posicion %d no puede ser nil", index)
		}
	}
	if len(s.configurationErrors) > 0 {
		return errors.Join(s.configurationErrors...)
	}
	return nil
}

// newGRPCServer construye el servidor gRPC nativo con las opciones configuradas.
func newGRPCServer(options []grpcgo.ServerOption) (server *grpcgo.Server, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			server = nil
			err = fmt.Errorf("error aplicando opciones del servidor grpc: %v", recovered)
		}
	}()
	return grpcgo.NewServer(options...), nil
}

// isNilInterface determina si una interfaz es nula o contiene un valor nulo.
func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Driver devuelve el servidor oficial. Se utiliza, por ejemplo, con las
// funciones RegisterXServer generadas por protoc. Su ciclo de vida debe
// administrarse mediante los métodos del wrapper, no con Stop o GracefulStop.
func (s *Server) Driver() *grpcgo.Server {
	if s == nil {
		return nil
	}
	return s.driver
}

// Address devuelve la dirección efectiva del listener cuando está disponible.
func (s *Server) Address() string {
	if s == nil {
		return ""
	}
	s.stateMutex.RLock()
	defer s.stateMutex.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.address
}

// ListenAndServe crea un listener mediante net.ListenConfig y delega en Serve.
func (s *Server) ListenAndServe() error {
	if s == nil || s.driver == nil {
		return ErrServerUnavailable
	}
	if s.closed.Load() {
		return ErrServerClosed
	}
	listener, err := s.listenConfig.Listen(context.Background(), s.network, s.address)
	if err != nil {
		return fmt.Errorf("error abriendo listener grpc: %w", err)
	}
	if err := s.Serve(listener); err != nil {
		_ = listener.Close()
		return err
	}
	return nil
}

// Serve sirve solicitudes en el listener proporcionado y bloquea hasta que el
// driver se detenga o el listener falle.
func (s *Server) Serve(listener net.Listener) error {
	if listener == nil || isNilInterface(listener) {
		return ErrNilListener
	}
	if s == nil || s.driver == nil {
		return ErrServerUnavailable
	}

	s.stateMutex.Lock()
	if s.closed.Load() {
		s.stateMutex.Unlock()
		return ErrServerClosed
	}
	if s.running {
		s.stateMutex.Unlock()
		return ErrServerRunning
	}
	if s.served {
		s.stateMutex.Unlock()
		return ErrServerAlreadyServed
	}
	s.listener = listener
	s.running = true
	s.served = true
	s.stateMutex.Unlock()

	err := s.driver.Serve(listener)

	s.stateMutex.Lock()
	s.running = false
	s.stateMutex.Unlock()

	if err == nil || (s.closed.Load() && errors.Is(err, grpcgo.ErrServerStopped)) {
		return nil
	}
	return fmt.Errorf("error sirviendo grpc: %w", err)
}

// Shutdown detiene la aceptación de conexiones y espera los RPC activos. Si el
// contexto vence, inicia Stop de forma asíncrona para forzar el cierre de
// transports y RPC pendientes sin bloquear más allá del contexto del llamador.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrNilContext
	}
	if s.driver == nil {
		return ErrServerUnavailable
	}

	s.closed.Store(true)
	s.shutdownOnce.Do(func() {
		go func() {
			s.driver.GracefulStop()
			s.signalShutdownDone()
		}()
	})

	select {
	case <-s.shutdownDone:
		return nil
	case <-ctx.Done():
		s.forceStopOnce.Do(func() {
			go func() {
				s.driver.Stop()
				s.signalShutdownDone()
			}()
		})
		return ctx.Err()
	}
}

// signalShutdownDone notifica que el proceso de apagado del servidor terminó.
func (s *Server) signalShutdownDone() {
	s.doneOnce.Do(func() {
		close(s.shutdownDone)
	})
}
