# Ejecutor SSH

Aplicacion web en Go para ejecutar un conjunto limitado de comandos en un servidor Windows mediante SSH, con interfaz completa para gestionar configuracion, usuarios y llaves SSH.

## Caracteristicas

- **Interfaz web** con 5 modulos: Maquinas virtuales (con Red y Documentacion), Comandos, Llaves SSH, Usuarios y Configuracion; tema claro y oscuro
- **Configuracion centralizada** mediante archivo `config.properties` (sin necesidad de editar codigo)
- **API REST completa** para todas las operaciones
- **Generacion de llaves SSH** ED25519 seguras
- **Gestion de usuarios** locales en Windows
- **Validacion de comandos** mediante lista blanca
- **Autenticacion SSH** por llave publica
- **Thread-safe** con proteccion mediante mutex

## Arquitectura

### Flujo de una solicitud

1. **Navegador** envía solicitud HTTP (comando, cambio de config, crear usuario, generar llave)
2. **main.go** enruta la solicitud según la ruta (/execute, /api/config, /api/keys, /api/users)
3. **handlers/** procesa la solicitud:
   - `command.go`: Ejecuta comandos SSH validados
   - `config_handler.go`: Lee/guarda configuracion
   - `keys_handler.go`: Genera/lista/descarga llaves SSH
4. **Modulos internos** realizan operaciones:
   - `config/`: Carga y persiste propiedades
   - `sshclient/`: Abre sesion SSH y ejecuta comandos
   - `sshkeys/`: Genera y maneja llaves ED25519
   - `users/`: Crea/elimina usuarios locales Windows
5. **Respuesta JSON** vuelve al navegador

### Estructura de archivos

```
ejecutor-ssh/
├── config.properties          # Archivo centralizado de propiedades
├── main.go                    # Punto de entrada y rutas HTTP
├── go.mod / go.sum           # Dependencias y versiones
│
├── config/
│   └── config.go              # Lee, parsea y persiste propiedades
│
├── sshclient/
│   └── client.go              # Cliente SSH con autenticacion por llave
│
├── sshkeys/
│   └── keys.go                # Generacion y almacenamiento de llaves ED25519
│
├── users/
│   └── users.go               # Creacion/eliminacion de usuarios Windows
│
├── handlers/
│   ├── command.go             # POST /execute - Ejecutar comandos
│   ├── config_handler.go      # GET/PUT /api/config - Gestionar configuracion
│   ├── keys_handler.go        # APIs para usuarios y llaves SSH
│   └── router.go              # Enrutador centralizado para /api/*
│
├── templates/
│   └── index.html             # Interfaz web con 4 pestanas
│
└── static/
    ├── style.css              # Estilos CSS responsive
    └── app.js                 # Logica JavaScript y llamadas API
```

## Configuracion

### Requisitos previos

- Go 1.22 o posterior
- Acceso a un servidor SSH configurado con autenticacion por llave publica
- En Windows: permisos de administrador para crear usuarios locales

### Instalacion

1. Clonar o descargar el proyecto
2. Descargar dependencias:

```powershell
go mod tidy
```

3. Compilar:

```powershell
go build
```

### Propiedades de configuracion

El archivo `config.properties` contiene todas las opciones. Se puede editar directamente o usar la API REST. Las variables de entorno sobrescriben los valores del archivo.

**Propiedades principales:**

```properties
# Servidor web
web.addr = :8081
web.port = 8081

# Conexion SSH
ssh.host = 192.168.56.11
ssh.port = 22
ssh.user = sshweb
ssh.keyPath = ~/.ssh/id_ed25519
ssh.knownHostsPath = ~/.ssh/known_hosts

# Shell remoto
shell.type = cmd
shell.startingPath = C:\

# Almacenamiento
keys.storePath = ./keys
users.storePath = ./users

# Comandos permitidos (lista separada por comas)
commands.allowed = dir,hostname,whoami,ipconfig,systeminfo,tasklist,netstat

# Seguridad
request.maxSize = 4096
```

### Iniciar la aplicacion

```powershell
cd ejecutor-ssh
go run .
```

Luego accede a `http://localhost:8080` en tu navegador.

## Laboratorio: como se conecta la app al hipervisor

La VM `ubuntu-virtualization-host` (VirtualBox) es el hipervisor libvirt. Su tarjeta en modo puente sobre Wi-Fi no siempre obtiene IP por DHCP (depende de la red Wi-Fi), asi que la app **no** depende de ella: usa la tarjeta NAT de esa VM con un reenvio de puerto de VirtualBox.

```powershell
# Una sola vez (queda guardado en la VM):
VBoxManage controlvm ubuntu-virtualization-host natpf2 "ssh-app,tcp,127.0.0.1,2222,,22"
```

```properties
vm.ssh.host=127.0.0.1
vm.ssh.port=2222
```

`known_hosts` debe tener la llave del host como `[127.0.0.1]:2222`. El cliente SSH solo negocia los tipos de llave que `known_hosts` conoce para ese host (evita el error `knownhosts: key mismatch` cuando solo se confio en la ED25519).

Los datos del hipervisor se editan en **Configuracion → Hipervisor** (boton *Probar conexion*) y se aplican sin reiniciar. La pastilla de la barra superior muestra si la app esta conectada.

Para entrar a una VM desde Windows, cada tarjeta de VM muestra el comando listo para copiar, saltando por el hipervisor:

```
ssh -i keys/<vm>-ssh -J vboxuser@127.0.0.1:2222 ubuntu@192.168.122.x
```

### Crear una VM

La VM queda **encendida** despues de crearla: `virt-install --cloud-init` conecta el disco de cloud-init solo durante el primer arranque, y en ese arranque se crean el usuario, la llave y se instala `qemu-guest-agent`. A los 3-4 minutos aparece su IP y el panel de **Red** ya puede leerla. El tamano de disco pedido se aplica al disco derivado y la distribucion se guarda en la descripcion de la VM.

### Distribuciones

Cada distribucion es una **plantilla** (imagen de nube oficial con cloud-init) en `/var/lib/libvirt/templates/` del hipervisor; no hace falta otro hipervisor. La app reconoce: `ubuntu-24.04`, `debian-12`, `debian-13`, `fedora-42`, `arch`, `rocky-9` (la detecta por el nombre del archivo de la plantilla). Las plantillas se descargan con su suma oficial verificada (SHA512 en Debian, SHA256 en Fedora y Arch) y se copian con `sudo qemu-img convert`.

### Redes virtuales (sub-pestaña "Redes virtuales")

- `GET /api/networks`: redes de libvirt con modo (`nat`, `route`, `isolated`), subred, DHCP, bridge y VMs conectadas.
- `POST /api/networks` `{"name":"aislada","mode":"isolated","subnet":"192.168.100.0/24","dhcp":true}`: la define, la arranca y la marca para arranque automatico. Rechaza subredes publicas o que se crucen con otra red.
- `DELETE /api/networks/{nombre}`: solo si ninguna VM la usa; `default` no se borra.
- `POST /api/vms` acepta `"networks": ["default", "aislada"]`: una tarjeta virtio por red; la primera debe ser NAT (el primer arranque necesita internet).
- `POST /api/vms/{nombre}/interfaces` `{"network":"..."}` y `DELETE /api/vms/{nombre}/interfaces/{mac}`: agregar o quitar tarjetas (en caliente si la VM esta encendida).

En una red **aislada** el DHCP no entrega puerta de enlace; el panel de Red lo sabe y no espera una ruta por defecto por esa tarjeta.

### Endpoints agregados

- `GET /api/users`: cuentas locales de Windows (`Get-LocalUser`). `POST` y `DELETE /api/users` exigen el header `X-API-Key`.
- `GET /api/keys/{nombre}/public`: llave publica (no es secreta).
- `GET/PUT /api/config` ahora incluye `vm.ssh.host`, `vm.ssh.port`, `vm.ssh.user`, `vm.ssh.keyPath` y `vm.ssh.knownHostsPath`.

## APIs REST

### 1. Ejecutar Comandos

#### POST /execute

Ejecuta un comando validado por SSH en el servidor remoto.

**Request:**
```json
{
  "command": "dir C:\\"
}
```

**Response (200 OK):**
```json
{
  "output": "Volume in drive C has no label..."
}
```

**Response (400 Bad Request):**
```json
{
  "error": "Comando no autorizado: del"
}
```

**Validaciones:**
- El comando debe estar en la lista `commands.allowed`
- Se rechaza comandos con operadores shell: `&`, `|`, `;`, `>`
- Tamaño maximo del body: 4096 bytes

---

### 2. Configuracion

#### GET /api/config

Obtiene la configuracion actual en formato JSON.

**Response (200 OK):**
```json
{
  "webAddr": ":8080",
  "sshHost": "192.168.56.11",
  "sshPort": 22,
  "sshUser": "sshweb",
  "shellType": "cmd",
  "allowedCommands": ["dir", "hostname", "whoami"]
}
```

#### PUT /api/config

Actualiza propiedades de configuracion y las guarda en `config.properties`.

**Request:**
```json
{
  "sshHost": "192.168.56.12",
  "sshPort": "2222"
}
```

**Response (200 OK):**
```json
{
  "message": "Configuracion actualizada",
  "webAddr": ":8080",
  "sshHost": "192.168.56.12",
  "sshPort": 2222
}
```

**Propiedades actualizables:**
- `sshHost`, `sshPort`, `sshUser`, `sshKeyPath`
- `shellType`, `shellStartingPath`
- `allowedCommands`

---

### 3. Llaves SSH

#### POST /api/keys

Genera una nueva llave SSH ED25519.

**Request:**
```json
{
  "keyName": "llave-servidor1",
  "username": "sshweb"
}
```

**Response (201 Created):**
```json
{
  "success": true,
  "message": "Llave generada exitosamente",
  "data": {
    "keyName": "llave-servidor1",
    "privatePath": "./keys/llave-servidor1",
    "publicPath": "./keys/llave-servidor1.pub",
    "algorithm": "ED25519"
  }
}
```

**Caracteristicas:**
- Genera par de llaves ED25519 (seguras y compactas)
- Llave privada guardada con permisos 0600 (solo lectura para propietario)
- Llave publica en formato `authorized_keys`

#### GET /api/keys

Lista todas las llaves SSH disponibles.

**Response (200 OK):**
```json
{
  "success": true,
  "message": "Llaves listadas",
  "data": [
    {
      "name": "llave-servidor1",
      "algorithm": "ED25519",
      "path": "./keys/llave-servidor1"
    },
    {
      "name": "llave-servidor2",
      "algorithm": "ED25519",
      "path": "./keys/llave-servidor2"
    }
  ]
}
```

#### GET /api/keys/{name}/download

Descarga la llave privada como archivo adjunto. **Requiere autenticación.**

**Request:** header obligatorio
```
X-API-Key: <token configurado en security.apiToken>
```

**Response (200 OK):** Archivo binario con header:
```
Content-Disposition: attachment; filename="llave-servidor1"
```

**Response (401 Unauthorized):** si falta el header, el token es inválido, o `security.apiToken` no está configurado (la ruta queda deshabilitada por defecto).

#### DELETE /api/keys/{name}

Elimina una llave (privada y publica). **Requiere autenticación** (mismo header que la descarga).

**Request:** header obligatorio
```
X-API-Key: <token configurado en security.apiToken>
```

**Response (200 OK):**
```json
{
  "success": true,
  "message": "Llave eliminada"
}
```

---

### 4. Usuarios Locales

#### POST /api/users

Crea un nuevo usuario local en Windows.

**Request:**
```json
{
  "username": "sshweb",
  "password": "Contraseña123!",
  "fullName": "Usuario SSH Web"
}
```

**Response (201 Created):**
```json
{
  "success": true,
  "message": "Usuario creado",
  "data": {
    "username": "sshweb"
  }
}
```

**Requisitos:**
- Solo en Windows (requiere permisos de administrador)
- Usa comando `net user` internally
- Validacion: usuario no debe existir

**Errores:**
- 409 Conflict: Usuario ya existe
- 500 Internal Server Error: Permisos insuficientes o error de sistema

#### DELETE /api/users/{username}

Elimina un usuario local.

**Response (200 OK):**
```json
{
  "success": true,
  "message": "Usuario eliminado"
}
```

**Requisitos:**
- Permisos de administrador
- Usuario no debe estar en sesion activa

---

## Seguridad

### Validacion de comandos

La aplicacion mantiene una lista blanca de comandos permitidos (`commands.allowed` en config.properties):

```properties
commands.allowed = dir,hostname,whoami,ipconfig,systeminfo
```

**Rechaza automaticamente:**
- Comandos no en la lista blanca
- Operadores shell: `&` (AND), `|` (pipe), `;` (separator), `>` (redireccionar)
- Rutas absolutas o relative traversal

Ejemplo de rechazos:
```
dir | findstr "file" Contiene pipe
del /s /q C:\  No en lista blanca
cd .. && format C:  Contiene &&
tasklist > output.txt  Contiene redireccionar
```

### Autenticacion de rutas sensibles

- `GET /api/keys/{name}/download` y `DELETE /api/keys/{name}` exigen el header `X-API-Key` con el valor de `security.apiToken` (o la variable de entorno `API_TOKEN`).
- Si `security.apiToken` no está configurado, la ruta queda **deshabilitada por defecto** (falla segura), no abierta.
- El token no se expone nunca por `GET /api/config` ni se puede modificar por `PUT /api/config`: solo se lee de `config.properties` o de la variable de entorno, para evitar que la propia API de configuración (sin autenticación) permita fijar un token propio y saltarse el control.
- La interfaz web pide el token una vez (con `prompt`) y lo guarda en `localStorage` del navegador para las siguientes descargas.

### Autenticacion SSH

- La llave privada permanece unicamente en el servidor web
- No se expone nunca al navegador cliente
- Verifica el servidor contra `known_hosts` para evitar MITM
- Soporta ED25519 (moderno y seguro)

### Permisos de archivos

- Llaves privadas: 0600 (solo propietario)
- Llaves publicas: 0644 (legible, escribible por propietario)
- Directorio `.ssh`: 0700 (acceso total solo propietario)

### Discos compartidos (shareable) en VMs

Un disco adicional puede marcarse como `shareable` para adjuntarlo a mas de una VM. La politica de concurrencia es **permitir libremente, solo avisar**:

- La aplicacion **no bloquea ni coordina** el acceso entre VMs. `shareable=on` (pasado a libvirt/QEMU) solo desactiva el locking de imagen que impediria el attach simultaneo; no evita que dos VMs escriban al mismo tiempo.
- Se asume que quien usa un disco compartido tiene un filesystem preparado para acceso concurrente (ej. GFS2, OCFS2) o coordina manualmente que VM escribe en cada momento.
- `POST /api/vms` devuelve un campo `warning` en la respuesta cuando la VM creada incluye al menos un disco `shareable`, para que la interfaz (o cualquier cliente de la API) lo muestre explicitamente.
- No se eligio bloquear el attach o mantener un lock a nivel de hipervisor porque el caso de uso valido de un disco compartido (clusters, coordinacion manual) requiere precisamente que varias VMs lo monten a la vez.

### Ciclo de vida de discos

- `GET /api/vms/{name}/disks` lista los discos definidos actualmente para una VM (leyendo la definicion en vivo de libvirt, no lo que se le pidio a `Create`), con nombre, ruta, tamaño, y si es de solo lectura o compartido.
- `DELETE /api/vms/{name}` **nunca borra un disco marcado como `shareable`**: antes de desvincular el dominio se listan sus discos, se hace `virsh undefine` sin `--remove-all-storage`, y solo se eliminan del disco los que no son compartidos. Si no se puede determinar la lista de discos (ej. el hipervisor no responde), el borrado se aborta en vez de arriesgarse a usar `--remove-all-storage` y destruir un disco compartido por error.
- Si al eliminar una VM se preservó algún disco compartido, la respuesta incluye un `warning` indicando cuáles, para que quien administra la VM sepa que ese espacio en disco sigue ocupado.

### Deteccion automatica de IP de las VMs

`GET /api/vms` (y por extension `Get`) rellenan el campo `ip` de cada VM en estado `running` probando dos fuentes, en orden:

1. **qemu-guest-agent** (`virsh domifaddr --source agent`) — el agente se instala y habilita **dentro de la VM** via cloud-init al crearla (paquete `qemu-guest-agent`, nunca en el hipervisor), y responde a traves de un canal virtio (`org.qemu.guest_agent.0`) que `Create` agrega a la definicion de la VM. Es la fuente mas confiable porque reporta lo que el sistema operativo de la VM ve realmente.
2. **Leases DHCP** (`virsh domifaddr --source lease`) — fallback si el agente no esta instalado o no responde. Usa el propio mecanismo de correlacion de libvirt contra los leases de la red gestionada (`network=default`), sin necesitar nada dentro de la VM.

Si ninguna fuente devuelve una IPv4 (VM apagada, agente no instalado y sin lease vigente), el campo `ip` queda vacio; nunca se falla la operacion por esto, ya que la IP es informativa. Para VMs creadas antes de este cambio, el agente no estara instalado hasta que se reaprovisionen o se instale manualmente `qemu-guest-agent` dentro de ellas.

### Configuracion de red de las VMs (boton "Red")

Cada VM encendida tiene un boton **Red** que abre un panel para cambiar su red: DHCP, IP fija o IP adicional, con mascara, puerta de enlace (principal, de respaldo, solo hacia ciertas redes o ninguna) y DNS.

- **Canal:** la app no entra a la VM por SSH. Usa `qemu-guest-agent` (`guest-exec` / `guest-exec-status`) desde el hipervisor, por el canal virtio. Ese canal no usa la red de la VM, asi que cambiar la IP no lo corta. Requiere que el agente permita `guest-exec` (en Ubuntu y Debian viene permitido; en RHEL/Rocky puede estar bloqueado por la configuracion de `qemu-ga`).
- **Lee antes de escribir:** un solo script de deteccion (`/etc/os-release`, `systemctl is-active`, `ip -br link`, `/sys/class/net`, `ip -4 addr`, `ip r`, DNS, `nmcli`, `arping`, cloud-init) decide el gestor de red: Netplan, NetworkManager, systemd-networkd o ifupdown. Desde el hipervisor se leen la red de libvirt, su rango DHCP, los leases y las reservas.
- **Valida en el servidor** (`mv/network_plan.go`): IP de red o broadcast, puerta de enlace fuera de la subred, IP prestada o reservada a otra VM, dos rutas por defecto con la misma prioridad, redes destino mal escritas, interfaces virtuales o Wi-Fi.
- **Aplica con reversion:** respalda, programa con `systemd-run` la vuelta atras dentro de la VM (`vm.network.revertSeconds`, 120 s por defecto, entre 30 y 600), escribe, aplica y verifica (`ip addr`, `ip r`, `ping` a la puerta de enlace, `getent`). Si algo falla, dispara la reversion de inmediato. Si la IP fija cae dentro del rango DHCP de libvirt, al final la reserva para la MAC de la tarjeta (`virsh net-update ... add ip-dhcp-host`).
- **Netplan + cloud-init:** no edita `50-cloud-init.yaml`; escribe `/etc/netplan/90-ejecutor-<interfaz>.yaml` y `/etc/cloud/cloud.cfg.d/99-disable-network-config.cfg`, para que cloud-init no vuelva a escribir la red al reiniciar.

Endpoints:

- `GET /api/vms/{name}/network`: lee la red de la VM y la guarda 10 minutos para las vistas previas.
- `POST /api/vms/{name}/network/plan`: valida una solicitud y devuelve las comprobaciones, los archivos, los comandos por fase y la tabla de rutas resultante. No ejecuta nada.
- `POST /api/vms/{name}/network/apply`: vuelve a leer la VM, valida y aplica. Devuelve cada fase con sus comandos y salidas, y el estado final releido.

Cuerpo de `plan` y `apply`: `{"interface":"enp1s0","mode":"static","ip":"192.168.122.50","mask":"/24","gateway":"192.168.122.1","role":"main","nets":"","dns":"192.168.122.1"}` (`mode`: `dhcp`, `static` o `extra`; `role`: `main`, `backup`, `routes` o `none`).

### Limite de solicitudes

- Tamaño maximo de body: 4096 bytes (configurable)
- Valida Content-Type: application/json
- Decodifica JSON de forma segura

### Consideraciones adicionales

- No ejecuta scripts (.ps1, .bat, .exe)
- No permite redireccionamientos
- No permite pipes o encadenamiento
- Cada comando se ejecuta aislado
- Thread-safe con mutex para operaciones concurrentes

## Uso

### 1. Ejecutar comando

1. Abre `http://localhost:8080`
2. En pestana **Comandos**, ingresa: `dir C:\`
3. Presiona **Ejecutar**
4. Ve el resultado

### 2. Cambiar configuracion

1. Ve a pestana **Configuracion**
2. Edita campos (ej: Host SSH, Puerto)
3. Presiona **Guardar Cambios**
4. La configuracion se persiste en `config.properties`

### 3. Generar llave SSH

1. Ve a pestana **Llaves SSH**
2. Ingresa nombre: `mi-llave`
3. Ingresa usuario SSH: `sshweb`
4. Presiona **Generar Llave ED25519**
5. Aparece en la lista con opcion de **Descargar** o **Eliminar**

### 4. Crear usuario

1. Ve a pestana **Usuarios**
2. Ingresa username, password, nombre completo
3. Presiona **Crear Usuario**
4. Se crea en el sistema operativo Windows

## Troubleshooting

### Error: "Permission denied (publickey)"

- Verifica que la llave privada exista en `ssh.keyPath`
- Comprueba que el usuario existe en el servidor remoto
- Valida la configuracion SSH directamente: `ssh -i <llave> <usuario>@<host> "hostname"`

### Error: "Comando no autorizado"

- Agrega el comando a `commands.allowed` en `config.properties`
- Reinicia la aplicacion para recargar la configuracion
- O usa la API PUT /api/config para actualizar dinamicamente

### Error: "User already exists" (al crear usuario)

- El usuario ya existe en Windows
- Usa diferente nombre o elimina primero con DELETE /api/users/{username}

### Puerto ya en uso

- Cambia `web.port` en `config.properties`
- O mata el proceso: `netstat -ano | findstr :8080` y `taskkill /PID <id> /F`

## Desarrollo

Instalar dependencias y ejecutar tests:

```powershell
go mod tidy
go test ./...
```

Compilar para produccion:

```powershell
go build -o ejecutor-ssh.exe
```

Luego ejecutar: `.\ejecutor-ssh.exe`

## Licencia

Este proyecto fue desarrollado como trabajo de grado. Consulta los terminos de uso en tu institucion.