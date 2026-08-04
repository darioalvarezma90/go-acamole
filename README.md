# go-acamole

`go-acamole` es un módulo con utilidades reutilizables para aplicaciones Go,
principalmente orientadas a microservicios, aunque no limitadas a ellos.

Todos los wrappers siguen la misma filosofía: configuración mediante opciones
funcionales, validación durante la construcción, tipos seguros para uso
concurrente, ciclo de vida explícito y acceso al driver oficial para evitar
duplicar APIs maduras.

Actualmente incluye:

- [`logger`](./logger), registros estructurados sobre Zap;
- [`mongodb`](./mongodb), cliente MongoDB con TLS y ciclo de vida administrado;
- [`postgresql`](./postgresql), pool PostgreSQL basado en pgx;
- [`rabbitmq`](./rabbitmq), servidor concurrente de consumidores AMQP;
- [`grpc`](./grpc), servidor gRPC con listener configurable y apagado graceful.

## Instalación

El módulo requiere Go 1.26. Instale únicamente los paquetes que vaya a utilizar:

```bash
go get github.com/darioalvarezma90/go-acamole/logger
go get github.com/darioalvarezma90/go-acamole/mongodb
go get github.com/darioalvarezma90/go-acamole/postgresql
go get github.com/darioalvarezma90/go-acamole/rabbitmq
go get github.com/darioalvarezma90/go-acamole/grpc
```

## Paquete `logger`

El paquete permite:

- registrar mensajes con niveles `Debug`, `Info`, `Warn`, `Error` y `Fatal`;
- generar una línea JSON por evento o usar texto legible;
- escribir en consola, archivo o ambos destinos;
- crear un archivo por día y segmentos adicionales al alcanzar el tamaño máximo;
- comprimir segmentos terminados con gzip;
- eliminar respaldos por antigüedad y por cantidad máxima;
- utilizar una misma instancia de `Logger` desde distintas goroutines;
- forzar la persistencia con `Sync` y liberar los recursos con `Close`.

Los argumentos que acompañan al mensaje se proporcionan como pares de clave y
valor:

```go
log.Info("pedido procesado", "order_id", orderID, "duration_ms", duration.Milliseconds())
```

La interfaz pública contiene únicamente los métodos de registro sin contexto:

```go
type Interface interface {
	Debug(message string, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message string, args ...any)
	Fatal(message string, args ...any)
}
```

### Uso básico

```go
package main

import (
	"log"

	"github.com/darioalvarezma90/go-acamole/logger"
)

func main() {
	applicationLogger, err := logger.NewLogger("orders-api")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := applicationLogger.Close(); err != nil {
			log.Printf("cerrar logger: %v", err)
		}
	}()

	applicationLogger.Info("servicio iniciado", "port", 8080)
	applicationLogger.Error(
		"no fue posible procesar el pedido",
		"order_id", "order-123",
	)
}
```

La configuración predeterminada escribe en `os.Stdout`, utiliza nivel `Info` y
codificación JSON. Si el nombre de la aplicación está vacío, se utiliza
`go-app`.

### Consola y archivos con rotación

```go
rotation, err := logger.NewRotation(
	logger.WithMaxSizeMB(50),
	logger.WithMaxBackups(10),
	logger.WithMaxAgeDays(30),
	logger.WithCompression(true),
	logger.WithLocalTime(true),
)
if err != nil {
	return err
}

applicationLogger, err := logger.NewLogger(
	"orders-api",
	logger.WithOutput(logger.ConsoleAndFileOutput),
	logger.WithLevel(logger.DebugLevel),
	logger.WithEncoding(logger.JSONEncoding),
	logger.WithDirectory("./logs"),
	logger.WithFileName("orders.log"),
	logger.WithRotation(rotation),
)
if err != nil {
	return err
}
defer applicationLogger.Close()
```

Con esa configuración se generan nombres como:

```text
logs/orders-03-08-2026.log
logs/orders-03-08-2026-01.log.gz
logs/orders-04-08-2026.log
```

La extensión `.log` en `WithFileName` es opcional. El paquete agrega
automáticamente la fecha y, cuando corresponde, el índice del segmento.

### Opciones disponibles

Las opciones principales de `NewLogger` son:

| Opción | Descripción |
| --- | --- |
| `WithOutput` | Selecciona `ConsoleOutput`, `FileOutput` o `ConsoleAndFileOutput`. |
| `WithLevel` | Define el nivel mínimo: `DebugLevel`, `InfoLevel`, `WarnLevel` o `ErrorLevel`. |
| `WithEncoding` | Selecciona `JSONEncoding` o `TextEncoding`. |
| `WithConsoleWriter` | Reemplaza `os.Stdout` por otro `io.Writer`. |
| `WithDirectory` | Define el directorio de archivos; el valor predeterminado es `./logs`. |
| `WithFileName` | Define el nombre base; de forma predeterminada se usa el nombre de la aplicación. |
| `WithRotation` | Aplica una configuración creada mediante `NewRotation`. |

Los valores predeterminados de rotación son:

| Propiedad | Valor predeterminado |
| --- | --- |
| Tamaño máximo | 100 MB por segmento |
| Respaldos | 5 archivos terminados |
| Antigüedad | 14 días |
| Compresión | gzip habilitado |
| Zona horaria de nombres | UTC |

Un valor `0` en respaldos o antigüedad deshabilita ese límite. La limpieza se
ejecuta al abrir un archivo diario o crear un nuevo segmento. Una entrada
individual puede superar el tamaño máximo porque nunca se divide entre archivos.

### Ciclo de vida y consideraciones

- Se debe llamar a `Close` para sincronizar y cerrar el archivo activo. `Close`
  es idempotente y puede invocarse desde distintas goroutines.
- `Sync` fuerza la persistencia del archivo activo sin cerrar el logger.
- Después de `Close`, las nuevas llamadas de registro se ignoran.
- `Fatal` registra el mensaje, cierra el logger y termina el proceso mediante
  `os.Exit(1)`; por ello, los `defer` pendientes de la aplicación no se ejecutan.
- Dentro de un mismo proceso no se permiten dos loggers que escriban en la misma
  combinación de directorio y nombre base. Esta protección no coordina procesos
  distintos.
- La compresión y la limpieza se realizan durante la rotación, por lo que esa
  escritura puede tardar más que una escritura normal.

## Paquete `mongodb`

`mongodb` administra un `*mongo.Client` del driver oficial v2 y la base de datos
seleccionada. `NewClient` crea el driver y, de forma predeterminada, ejecuta
`Ping` para verificar conectividad antes de devolverlo.

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/darioalvarezma90/go-acamole/mongodb"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongodb.NewClient(
		ctx,
		"mongodb://localhost:27017",
		"orders",
		mongodb.WithAppName("orders-api"),
		mongodb.WithStableAPI(),
		mongodb.WithMaxPoolSize(50),
		mongodb.WithServerSelectionTimeout(5*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := client.Close(closeCtx); err != nil {
			log.Printf("cerrar mongodb: %v", err)
		}
	}()

	orders := client.Database().Collection("orders")
	_ = orders
}
```

### TLS mutuo y autenticación X.509

`NewTLS` carga una CA, certificado cliente y llave privada PEM. Acepta llaves
PKCS#8 sin cifrar o `ENCRYPTED PRIVATE KEY`; la contraseña se conserva sólo
durante la construcción y la copia interna se sobrescribe después.

```go
tlsConfiguration, err := mongodb.NewTLS(
	"./certs/ca.pem",
	"./certs/client.pem",
	"./certs/client-key.pem",
	mongodb.WithClientKeyPassword([]byte(os.Getenv("MONGODB_KEY_PASSWORD"))),
	mongodb.WithTLSServerName("mongodb.internal"),
)
if err != nil {
	return err
}

client, err := mongodb.NewClient(
	ctx,
	"mongodb://mongodb.internal:27017/?authSource=$external",
	"orders",
	mongodb.WithTLS(tlsConfiguration),
	mongodb.WithX509Authentication(),
)
```

Opciones de `NewClient`:

| Opción | Descripción |
| --- | --- |
| `WithConnectionCheck` | Habilita o deshabilita el `Ping` inicial; está habilitado por defecto. |
| `WithPingReadPreference` | Configura la preferencia de lectura utilizada por `Ping`. |
| `WithAppName` | Envía el nombre de la aplicación al deployment. |
| `WithStableAPI` | Habilita MongoDB Stable API v1. |
| `WithConnectTimeout` | Limita el establecimiento de conexiones de red. |
| `WithServerSelectionTimeout` | Limita la selección de un servidor adecuado. |
| `WithOperationTimeout` | Define un timeout para operaciones sin deadline propio. |
| `WithMinPoolSize`, `WithMaxPoolSize` | Configuran el pool por servidor. |
| `WithTLS` | Aplica una configuración creada por `NewTLS`. |
| `WithX509Authentication` | Usa el certificado cliente como identidad MongoDB-X509. |
| `WithDriverOptions` | Agrega opciones avanzadas del driver, salvo otro URI. |

`Driver` y `Database` exponen los tipos oficiales; no debe llamarse
`Disconnect` directamente sobre `Driver`. `Close` es idempotente y conserva el
resultado del primer intento. `Ping` rechaza contextos `nil` y clientes cerrados.

## Paquete `postgresql`

`postgresql` administra un `*pgxpool.Pool`. Acepta cadenas de conexión URL o
libpq, valida la configuración resultante y ejecuta `Ping` por defecto.

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/darioalvarezma90/go-acamole/postgresql"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := postgresql.NewClient(
		ctx,
		"postgres://app:secret@localhost:5432/orders?sslmode=verify-full",
		postgresql.WithApplicationName("orders-api"),
		postgresql.WithMaxConnections(20),
		postgresql.WithMinConnections(2),
		postgresql.WithConnectTimeout(5*time.Second),
		postgresql.WithRequireTLS(),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	var value int
	if err := client.Driver().QueryRow(ctx, "select 1").Scan(&value); err != nil {
		log.Fatal(err)
	}
}
```

Opciones de `NewClient`:

| Opción | Descripción |
| --- | --- |
| `WithConnectionCheck` | Habilita o deshabilita el `Ping` inicial; está habilitado por defecto. |
| `WithApplicationName` | Configura `application_name`. |
| `WithConnectTimeout` | Limita la apertura de una conexión. |
| `WithMaxConnections` | Establece el máximo del pool. |
| `WithMinConnections` | Establece el mínimo del pool. |
| `WithMinIdleConnections` | Establece el mínimo de conexiones inactivas. |
| `WithMaxConnectionLifetime` | Limita la vida de cada conexión. |
| `WithMaxConnectionLifetimeJitter` | Distribuye el vencimiento de conexiones. |
| `WithMaxConnectionIdleTime` | Limita el tiempo inactivo. |
| `WithHealthCheckPeriod` | Configura la frecuencia de revisión del pool. |
| `WithPingTimeout` | Limita los health checks internos. |
| `WithTLSConfig` | Aplica `tls.Config`, exige TLS y elimina fallbacks sin cifrado. |
| `WithRequireTLS` | Rechaza configuraciones que todavía permitan transporte sin TLS. |
| `WithPoolConfigurer` | Modifica de forma avanzada el `pgxpool.Config` antes de validarlo. |

`WithTLSConfig` rechaza `InsecureSkipVerify`, exige TLS 1.2 o superior y deriva
`ServerName` de cada host cuando no se proporciona. `WithRequireTLS` sólo exige
transporte cifrado; para validar también hostname y confianza use
`sslmode=verify-full` o `WithTLSConfig`.

`Driver` no debe cerrarse directamente. `Close` es idempotente, seguro entre
goroutines y espera que se liberen las conexiones adquiridas del pool.

## Paquete `rabbitmq`

El paquete implementa un servidor de consumidores sobre el driver oficial
[`amqp091-go`](https://github.com/rabbitmq/amqp091-go). La conexión se establece
en `NewServer`; la topología y los consumidores se inician al llamar `Serve`.
Cada worker utiliza un canal AMQP dedicado.

```go
package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/darioalvarezma90/go-acamole/rabbitmq"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	server, err := rabbitmq.NewServer(
		"amqp://guest:guest@localhost:5672/",
		rabbitmq.WithConnectionName("orders-worker"),
		rabbitmq.WithTopologyConfigurer(func(channel *amqp.Channel) error {
			_, err := channel.QueueDeclare(
				"orders", true, false, false, false, nil,
			)
			return err
		}),
		rabbitmq.WithErrorHandler(func(err error) {
			log.Printf("mensaje rechazado: %v", err)
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	err = server.RegisterConsumer(
		"orders",
		func(ctx context.Context, delivery amqp.Delivery) error {
			return processOrder(ctx, delivery.Body)
		},
		rabbitmq.WithConsumerConcurrency(4),
		rabbitmq.WithPrefetch(8, 0, false),
		rabbitmq.WithRequeueOnError(false),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
```

Comportamiento relevante:

- Con `autoAck` deshabilitado, que es el valor predeterminado, el servidor hace
  `Ack` cuando el handler devuelve `nil` y `Nack` cuando devuelve un error.
- Los errores reencolan el mensaje de forma predeterminada. Para evitar ciclos
  con mensajes inválidos, configure una dead-letter queue o utilice
  `WithRequeueOnError(false)`.
- El `Nack` se envía antes de invocar `WithErrorHandler`, de modo que un callback
  lento no retiene el acknowledgement del broker. El callback debe retornar con
  rapidez y no debe usarse como worker adicional.
- Un pánico del handler se recupera, se informa mediante `WithErrorHandler` y se
  procesa como un error normal.
- `WithConsumerConcurrency` crea un canal independiente por worker;
  `WithPrefetch` se aplica a cada uno. Un consumidor exclusivo sólo admite
  concurrencia `1` y no puede combinarse con otro consumidor registrado para la
  misma cola.
- `Serve` sólo puede ejecutarse una vez. Al cancelar su contexto o llamar a
  `Shutdown`, se cancelan los consumidores, terminan los handlers cooperativos y
  se cierra la conexión. El cierre interno de `Serve` está limitado a cinco
  segundos; `Shutdown` respeta cancelación y deadline del contexto recibido.
- `Driver` expone `*amqp.Connection` para casos avanzados. Para cargas sostenidas
  conviene usar conexiones distintas para publicación y consumo, como recomienda
  el driver oficial. No cierre la conexión devuelta; use `Shutdown`.

Opciones del servidor:

| Opción | Descripción |
| --- | --- |
| `WithAMQPConfig` | Aplica una configuración avanzada del driver y copia TLS, properties y recuperación mutables. |
| `WithTLSConfig` | Configura TLS verificado para una URI `amqps`; exige TLS 1.2 o superior. |
| `WithHeartbeat` | Configura el heartbeat solicitado. |
| `WithConnectionName` | Define el nombre visible en RabbitMQ Management. |
| `WithTopologyConfigurer` | Declara exchanges, colas y bindings antes de consumir. |
| `WithErrorHandler` | Observa errores de handlers y pánicos recuperados. |

Opciones de cada consumidor:

| Opción | Descripción |
| --- | --- |
| `WithConsumerTag` | Define el identificador base del consumidor. |
| `WithConsumerConcurrency` | Crea el número indicado de canales y workers; el valor predeterminado es `1`. |
| `WithPrefetch` | Configura count, size y alcance de `basic.qos`; el count predeterminado es `1`. |
| `WithAutoAck` | Permite acknowledgement previo por el broker; está deshabilitado por defecto. |
| `WithExclusive` | Solicita un consumidor exclusivo y requiere concurrencia `1`. |
| `WithConsumerNoWait` | Omite la confirmación de `basic.consume`. |
| `WithConsumerArguments` | Agrega argumentos AMQP y copia tablas, slices y bytes mutables. |
| `WithRequeueOnError` | Controla el requeue tras error; está habilitado por defecto. |

## Paquete `grpc`

El paquete envuelve `grpc-go` sin duplicar la API generada por Protocol Buffers.
`Driver` devuelve `*grpc.Server`, sobre el que se registran los servicios antes
de iniciar el listener. No llame `Stop` o `GracefulStop` directamente sobre ese
valor; use `Shutdown` para mantener coherente el estado del wrapper.

```go
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	utilgrpc "github.com/darioalvarezma90/go-acamole/grpc"
	grpcgo "google.golang.org/grpc"
)

func main() {
	server, err := utilgrpc.NewServer(
		":50051",
		utilgrpc.WithUnaryInterceptors(requestLogger),
		utilgrpc.WithGRPCOptions(
			grpcgo.MaxRecvMsgSize(4 << 20),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	ordersv1.RegisterOrdersServer(server.Driver(), newOrdersService())

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()

	signals, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	select {
	case err := <-serveErrors:
		if err != nil {
			log.Fatal(err)
		}
	case <-signals.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("apagado grpc forzado: %v", err)
		}
	}
}
```

También puede proporcionarse un listener ya creado mediante `Serve`, por
ejemplo para sockets Unix, pruebas en memoria o activación de sockets. La
dirección efectiva está disponible con `Address`, incluso cuando se solicitó el
puerto `0`. `Shutdown` deja terminar los RPC activos; si vence el contexto,
inicia `grpc.Server.Stop` de forma asíncrona y devuelve inmediatamente el error
del contexto. Esto mantiene el límite solicitado incluso si se configuró
`grpc.WaitForHandlers(true)` y un handler no coopera con la cancelación.

Opciones de `NewServer`:

| Opción | Descripción |
| --- | --- |
| `WithGRPCOptions` | Agrega opciones nativas de `grpc-go`. |
| `WithUnaryInterceptors` | Encadena interceptores unary y rechaza valores `nil`. |
| `WithStreamInterceptors` | Encadena interceptores streaming y rechaza valores `nil`. |
| `WithNetwork` | Cambia la red de `ListenAndServe`; el valor predeterminado es `tcp`. |
| `WithListenConfig` | Usa un `net.ListenConfig` personalizado. |

Cada instancia es de un solo uso: después de iniciar `Serve`, no puede iniciarse
otra vez. `Serve` toma propiedad del listener y `grpc-go` lo cierra al terminar.

## Pruebas

Para ejecutar las pruebas del módulo:

```bash
go test ./...
```

Las integraciones externas se omiten cuando sus variables no están definidas:

```bash
POSTGRESQL_TEST_DSN='postgres://...' go test ./postgresql
RABBITMQ_TEST_URL='amqp://...' go test ./rabbitmq
```

Para incluir el detector de carreras, cuando el entorno tenga CGO y un compilador
de C disponibles:

```bash
go test -race ./...
```
