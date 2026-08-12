# go-acamole

`go-acamole` es un módulo con utilidades reutilizables para aplicaciones Go,
principalmente orientadas a microservicios, aunque no limitadas a ellos.

Todos los wrappers siguen la misma filosofía: configuración mediante opciones
funcionales, validación durante la construcción, tipos seguros para uso
concurrente, ciclo de vida explícito y acceso al driver oficial para evitar
duplicar APIs maduras.

Actualmente incluye:

- [`logger`](./logger), registros estructurados sobre Zap;
- [`mongodb`](./mongodb), cliente MongoDB con TLS y repositorios de colecciones BSON;
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

- registrar mensajes con niveles `Debug`, `Info`, `Warn` y `Error`;
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

La interfaz pública `ILogger` contiene únicamente los métodos de registro sin
contexto:

```go
type ILogger interface {
	Debug(message string, args ...any)
	Info(message string, args ...any)
	Warn(message string, args ...any)
	Error(message string, args ...any)
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

La configuración predeterminada escribe en `os.Stdout`, utiliza nivel `Debug` y
codificación JSON. Si el nombre de la aplicación está vacío, se utiliza
`go-app`; un nombre formado por espacios o con espacios laterales se rechaza.
Cada registro incluye el campo base `app` con ese nombre.

### Consola y archivos con rotación

```go
rotation, err := logger.NewRotation(
	logger.WithMaxSizeMB(50),
	logger.WithMaxBackups(10),
	logger.WithMaxAgeDays(30),
	logger.WithCompression(),
	logger.WithLocalTime(),
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
automáticamente la fecha y, cuando corresponde, el índice del segmento. El
nombre base no puede incluir directorios.

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

`ParseLogLevel` interpreta, sin distinguir mayúsculas, `debug`, `info`, `warn`,
`warning` y `error`. `ParseLogOutput` acepta `console`, `file`, `file+console` y
`console+file`; ambos eliminan espacios laterales y devuelven error para valores
desconocidos.

Los valores predeterminados de rotación son:

| Propiedad | Valor predeterminado |
| --- | --- |
| Tamaño máximo | 100 MB por segmento |
| Respaldos | 5 archivos terminados |
| Antigüedad | 14 días |
| Compresión | deshabilitada |
| Zona horaria de nombres | UTC |

Un valor `0` en respaldos o antigüedad deshabilita ese límite. La limpieza se
ejecuta al abrir un archivo diario o crear un nuevo segmento. Una entrada
individual puede superar el tamaño máximo porque nunca se divide entre archivos.
`WithCompression()` habilita gzip para los segmentos terminados y
`WithLocalTime()` utiliza la zona horaria local en los nombres; sin esas opciones
se conservan archivos `.log` sin comprimir y se utiliza UTC.

### Ciclo de vida y consideraciones

- Se debe llamar a `Close` para sincronizar y cerrar el archivo activo. `Close`
  es idempotente y puede invocarse desde distintas goroutines.
- `Sync` fuerza la persistencia del archivo activo sin cerrar el logger.
- `MaintenanceError` permite consultar el último fallo al eliminar archivos por
  retención. Conserva el último fallo observado y estos errores no interrumpen
  las escrituras.
- Después de `Close`, las nuevas llamadas de registro se ignoran.
- El paquete no termina el proceso. La aplicación decide cuándo utilizar
  `log.Fatal`, `os.Exit` o propagar un error, después de cerrar sus recursos.
- Dentro de un mismo proceso no se permiten dos loggers que escriban en la misma
  combinación de directorio y nombre base. Esta protección no coordina procesos
  distintos.
- La compresión y la limpieza se realizan durante la rotación, por lo que esa
  escritura puede tardar más que una escritura normal.

## Paquete `mongodb`

`mongodb` administra un `*mongo.Client` del driver oficial v2 y la base de datos
seleccionada. El contrato del cliente se expone mediante `IClient`. `NewClient`
crea el driver y, de forma predeterminada, ejecuta `Ping` para verificar
conectividad antes de devolverlo. La URI y el nombre de la base de datos son
obligatorios y no pueden contener espacios laterales. Con
`WithConnectionCheck(false)` se omite esa verificación inicial, por lo que la
construcción no garantiza que el deployment sea alcanzable.

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

`NewTLS` carga una CA, un certificado cliente y una única llave privada PEM.
Acepta llaves PKCS#8, PKCS#1 RSA y SEC1 EC sin cifrar, además de PKCS#8 cifrado
como `ENCRYPTED PRIVATE KEY`. Rechaza el cifrado PEM legado y archivos con más
de una llave privada. La contraseña se conserva sólo durante la construcción y
la copia interna se sobrescribe después. La configuración resultante exige TLS
1.2 o superior y valida el servidor contra la CA proporcionada; un
`WithTLSServerName` no vacío sustituye el nombre inferido por el driver.

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
| `WithDriverOptions` | Agrega copias de opciones avanzadas del driver; no permite definir otro URI. |

`Driver` y `Database` exponen los tipos oficiales; no debe llamarse
`Disconnect` directamente sobre `Driver`. `Close` es idempotente y conserva el
resultado del primer intento, incluso si ese intento falla o su contexto vence;
no se realiza un segundo `Disconnect`. `Ping` rechaza contextos `nil` y clientes
cerrados.
Después de construir el driver, el wrapper elimina su copia de la URI para no
retener credenciales innecesariamente.

### Tipo `Repository`

`Repository` es un wrapper de `*mongo.Collection` para trabajar directamente
con los tipos BSON del driver, sin definir modelos Go ni utilizar genéricos. Se
construye a partir del `Client` y de un nombre de colección:

```go
orders, err := mongodb.NewRepository(client, "orders")
if err != nil {
	return err
}
```

El nombre no puede estar vacío ni contener espacios al inicio o al final. La
colección no tiene que existir previamente: MongoDB la creará al ejecutar la
primera escritura. El contrato público está definido por `IRepository`:

```go
type IRepository interface {
	Driver() *mongo.Collection
	Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) ([]bson.Raw, error)
	FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) (bson.Raw, error)
	FindByID(ctx context.Context, id bson.ObjectID, opts ...options.Lister[options.FindOneOptions]) (bson.Raw, error)
	Insert(ctx context.Context, documents any, opts ...options.Lister[options.InsertManyOptions]) (*mongo.InsertManyResult, error)
	InsertOne(ctx context.Context, document any, opts ...options.Lister[options.InsertOneOptions]) (*mongo.InsertOneResult, error)
}
```

| Método | Comportamiento |
| --- | --- |
| `Driver` | Devuelve `*mongo.Collection` para updates, deletes, agregaciones, índices, change streams u otras operaciones avanzadas. |
| `Find` | Ejecuta una búsqueda y devuelve una copia independiente de cada documento como `[]bson.Raw`. |
| `FindOne` | Devuelve un documento como `bson.Raw` y conserva `mongo.ErrNoDocuments` cuando no encuentra coincidencias. |
| `FindByID` | Busca por `_id` utilizando un `bson.ObjectID`. |
| `Insert` | Delega en `mongo.Collection.InsertMany` y devuelve `*mongo.InsertManyResult`. |
| `InsertOne` | Inserta un documento y devuelve `*mongo.InsertOneResult`. |

Los filtros y documentos individuales aceptan exclusivamente `bson.M`,
`bson.D` o un `bson.Raw` válido. Para buscar todos los documentos se utiliza un
documento vacío, por ejemplo `bson.D{}`, y no `nil`.

`Insert` acepta colecciones no vacías de tipo `[]bson.M`, `[]bson.D`,
`[]bson.Raw` o `[]any`; esta última permite combinar los tres tipos BSON. Tanto
`Find` como las inserciones aceptan las opciones funcionales oficiales del
driver.

```go
pendingOrders, err := orders.Find(
	ctx,
	bson.M{"status": "pending"},
	options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100),
)
if err != nil {
	return err
}
fmt.Println("pedidos pendientes:", len(pendingOrders))

_, err = orders.Insert(
	ctx,
	[]bson.M{
		{"status": "pending", "total": 1250},
		{"status": "pending", "total": 850},
	},
	options.InsertMany().SetOrdered(false),
)
```

Una inserción individual devuelve el `_id` exactamente con el tipo utilizado
por MongoDB o por el llamador. Cuando el driver genera el identificador, éste es
normalmente un `bson.ObjectID`:

```go
inserted, err := orders.InsertOne(ctx, bson.M{
	"status": "pending",
	"total":  1250,
})
if err != nil {
	return err
}

orderID, ok := inserted.InsertedID.(bson.ObjectID)
if !ok {
	return fmt.Errorf("_id insertado no es ObjectID")
}

order, err := orders.FindByID(ctx, orderID)
if err != nil {
	return err
}

status, ok := order.Lookup("status").StringValueOK()
if !ok {
	return fmt.Errorf("status no existe o no es string")
}
fmt.Println(status)
```

Los errores de validación pueden comprobarse con `errors.Is`. Los principales
son `ErrNilContext`, `ErrClientUnavailable`, `ErrClientClosed`,
`ErrRepoUnavailable` y `ErrInvalidDocument`. Los errores del driver se envuelven
sin perder su causa; por ejemplo, `errors.Is(err, mongo.ErrNoDocuments)` sigue
funcionando después de `FindOne` o `FindByID`.

`Repository` no tiene `Close`: no posee conexiones y el `Client` debe permanecer
abierto mientras se use cualquiera de sus repositorios. Su estado es inmutable
y `mongo.Collection` es segura para uso concurrente, por lo que distintas
goroutines pueden compartir la misma instancia sin serializar sus operaciones.
Si el cierre del cliente interrumpe una operación, el error conserva tanto
`ErrClientClosed` como la causa devuelta por el driver.

## Paquete `postgresql`

`postgresql` administra un `*pgxpool.Pool` mediante el contrato `IClient`.
Acepta cadenas de conexión URL o libpq, valida la configuración resultante y
ejecuta `Ping` por defecto. La cadena es obligatoria, no admite espacios
laterales y se elimina de la estructura del wrapper después de interpretarla.
Con `WithConnectionCheck(false)` se crea el pool sin comprobar que PostgreSQL
sea alcanzable.

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
| `WithConnectTimeout` | Limita la apertura de una conexión; cero deshabilita el timeout. |
| `WithMaxConnections` | Establece el máximo del pool. |
| `WithMinConnections` | Establece el mínimo del pool. |
| `WithMinIdleConnections` | Establece el mínimo de conexiones inactivas. |
| `WithMaxConnectionLifetime` | Limita la vida de cada conexión; cero deshabilita el límite. |
| `WithMaxConnectionLifetimeJitter` | Distribuye el vencimiento de conexiones; admite cero. |
| `WithMaxConnectionIdleTime` | Limita el tiempo inactivo; cero deshabilita el límite. |
| `WithHealthCheckPeriod` | Configura la frecuencia de revisión del pool. |
| `WithPingTimeout` | Limita los health checks internos; cero no agrega timeout. |
| `WithTLSConfig` | Aplica `tls.Config`, exige TLS y elimina fallbacks sin cifrado. |
| `WithRequireTLS` | Rechaza configuraciones que todavía permitan transporte sin TLS. |
| `WithPoolConfigurer` | Modifica de forma avanzada el `pgxpool.Config` antes de validarlo. |

`WithTLSConfig` rechaza `InsecureSkipVerify`, exige TLS 1.2 o superior y deriva
`ServerName` de cada host cuando no se proporciona. `WithRequireTLS` sólo exige
transporte cifrado; para validar también hostname y confianza use
`sslmode=verify-full` o `WithTLSConfig`.

Los `PoolConfigurer` se ejecutan en el orden registrado después de
`pgxpool.ParseConfig`. Si se usa `WithTLSConfig`, su copia segura se aplica
después de esos configuradores para impedir que restauren accidentalmente un
fallback sin TLS. El máximo del pool debe ser mayor que cero; mínimos y
duraciones no pueden ser negativos, el periodo de health check debe ser mayor
que cero y los mínimos no pueden superar el máximo.

`Driver` no debe cerrarse directamente. `Close` es idempotente, seguro entre
goroutines, no devuelve error y espera que se liberen las conexiones adquiridas
del pool. `Ping` rechaza contextos `nil` y distingue `ErrClientUnavailable` de
`ErrClientClosed`; estos errores pueden comprobarse con `errors.Is`.

## Paquete `rabbitmq`

El paquete implementa, mediante el contrato `IServer`, un servidor de
consumidores sobre el driver oficial
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
		rabbitmq.WithEventHandler(func(event rabbitmq.Event) {
			log.Printf("evento rabbitmq: tipo=%s cola=%s error=%v", event.Type, event.Queue, event.Err)
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
		func(_ context.Context, delivery amqp.Delivery) error {
			log.Printf("pedido recibido: %s", delivery.Body)
			return nil
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
- Con `autoAck` habilitado, el broker confirma el mensaje antes de entregarlo:
  el servidor todavía informa errores del handler, pero ya no puede ejecutar
  `Nack` ni recuperar una entrega fallida. En ese modo tampoco aplica QoS.
- Los errores reencolan el mensaje de forma predeterminada. Para evitar ciclos
  con mensajes inválidos, configure una dead-letter queue o utilice
  `WithRequeueOnError(false)`.
- El `Nack` se envía antes de invocar `WithErrorHandler`, de modo que un callback
  lento no retiene el acknowledgement del broker. El callback debe retornar con
  rapidez y no debe usarse como worker adicional.
- Un pánico del handler se recupera, se informa mediante `WithErrorHandler` y se
  procesa como un error normal.
- El handler no debe llamar `Ack`, `Nack` o `Reject`; el servidor es propietario
  de las confirmaciones. Los fallos de `Ack` o `Nack` son errores de
  infraestructura y hacen terminar `Serve`.
- `WithConsumerConcurrency` crea un canal independiente por worker;
  `WithPrefetch` se aplica a cada uno cuando `autoAck` está deshabilitado. Un
  consumidor exclusivo sólo admite concurrencia `1` y no puede combinarse con
  otro consumidor registrado para la misma cola.
- Los configuradores de topología se ejecutan en orden, sobre un canal dedicado,
  antes de crear consumidores. Los consumidores deben registrarse antes de
  `Serve`, y `Serve` devuelve `ErrNoConsumers` si no se registró ninguno.
- `Serve` sólo puede ejecutarse una vez. Al cancelar su contexto o llamar a
  `Shutdown`, se cancelan los consumidores, terminan los handlers cooperativos y
  se cierra la conexión. El cierre interno de `Serve` está limitado a cinco
  segundos; `Shutdown` respeta cancelación y deadline del contexto recibido.
- Los cierres concurrentes comparten una sola operación sobre el driver, pero
  cada llamada a `Shutdown` espera únicamente hasta el deadline de su contexto.
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
| `WithEventHandler` | Observa inicio/parada del servidor y consumidores, además de errores de infraestructura. |

`WithErrorHandler` recibe un `*HandlerError`, compatible con `errors.As`, que
incluye `Queue`, `ConsumerTag`, `DeliveryTag` y conserva la causa mediante
`Unwrap`. Puede ejecutarse concurrentemente desde distintos workers. Sus pánicos
se recuperan, pero debe ser seguro para concurrencia y retornar rápidamente.

`WithEventHandler` recibe valores `Event` y también puede ejecutarse
concurrentemente. Sus pánicos se recuperan; el callback debe ser seguro para
concurrencia y retornar rápidamente. `Event.Type` es un `EventType` y sus valores
posibles son:

| Tipo | Campos relevantes |
| --- | --- |
| `EventServerStarted` | Todos los consumidores se iniciaron. |
| `EventServerStopped` | `Err` contiene el resultado final de `Serve`, si existe. |
| `EventConsumerStarted`, `EventConsumerStopped` | `Queue` y `ConsumerTag` identifican el worker. |
| `EventInfrastructureError` | `Err` contiene un fallo no causado por cancelación normal del contexto. |

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

Los errores de ciclo de vida exportados son `ErrNilContext`, `ErrServerClosed`,
`ErrServerRunning`, `ErrServerAlreadyServed`, `ErrServerUnavailable` y
`ErrNoConsumers`. Los errores de contexto y las causas del driver se conservan
para `errors.Is`; `HandlerError` conserva su causa y puede identificarse con
`errors.As`.

## Paquete `grpc`

El paquete envuelve `grpc-go` mediante el contrato `IServer`, sin duplicar la
API generada por Protocol Buffers. `Driver` devuelve `*grpc.Server`, sobre el que
se registran los servicios antes de iniciar el listener. No llame `Stop` o
`GracefulStop` directamente sobre ese valor; use `Shutdown` para mantener
coherente el estado del wrapper.

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
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	server, err := utilgrpc.NewServer(
		":50051",
		utilgrpc.WithGRPCOptions(
			grpcgo.MaxRecvMsgSize(4 << 20),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	healthpb.RegisterHealthServer(server.Driver(), health.NewServer())

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
`ListenAndServe` crea el listener con la red configurada, mientras que `Serve`
acepta uno ya creado y rechaza valores `nil`.

Los errores de ciclo de vida exportados son `ErrNilContext`, `ErrNilListener`,
`ErrServerClosed`, `ErrServerRunning`, `ErrServerAlreadyServed` y
`ErrServerUnavailable`. `Shutdown` es idempotente y seguro para llamadas
concurrentes; una vez iniciado el apagado, la instancia queda cerrada aunque
nunca haya llegado a servir solicitudes.

## Pruebas

Para ejecutar las pruebas del módulo:

```bash
go test ./...
```

Las integraciones externas se omiten cuando sus variables no están definidas:

```bash
MONGODB_TEST_URI='mongodb://...' go test ./mongodb
POSTGRESQL_TEST_DSN='postgres://...' go test ./postgresql
RABBITMQ_TEST_URL='amqp://...' go test ./rabbitmq
```

Para incluir el detector de carreras, cuando el entorno tenga CGO y un compilador
de C disponibles:

```bash
go test -race ./...
```

El directorio [`examples`](./examples) contiene programas compilables para cada
paquete. La compatibilidad y los cambios sin publicar se documentan en
[`COMPATIBILITY.md`](./COMPATIBILITY.md) y [`CHANGELOG.md`](./CHANGELOG.md).

Las interfaces exportadas se conservan para quienes deseen utilizarlas, aunque
en código de aplicación suele ser preferible declarar interfaces pequeñas junto
al consumidor y depender sólo de los métodos necesarios.
