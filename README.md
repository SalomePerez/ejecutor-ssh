# Ejecutor SSH

Aplicación web en Go que **crea máquinas virtuales y automatiza la configuración de su red** (IP fija, DHCP, IP adicional, rutas, reenvío IPv4) en distribuciones Linux de distintas familias, de forma remota y sin cortar la comunicación al cambiar la IP.

El hipervisor es **VirtualBox**, en el mismo equipo que la aplicación. La app lo controla con `VBoxManage`.

```
Equipo físico (Windows)
├── Navegador ──HTTP──► App web (Go, :8081 por defecto)
│                          │ VBoxManage (local)
│                          ▼
└── VirtualBox ──► VM Debian · VM Ubuntu · VM Fedora · VM Arch …
                    (NAT, puente, solo anfitrión, redes internas)
```

**Para ponerla a funcionar en otro equipo, siga [Instalación y puesta en marcha](#instalación-y-puesta-en-marcha).** En resumen: instalar VirtualBox y Go, copiar `config.properties.example` a `config.properties` y llenar el usuario y la contraseña de las VMs, poner al menos una plantilla `.vdi`, ejecutar `go run .` y abrir http://localhost:8081.

## Qué hace

- **Máquinas virtuales**: crear desde plantillas (imágenes oficiales de nube con cloud-init), encender, apagar, editar CPU y memoria, renombrar, eliminar, discos adicionales y compartidos.
- **Tarjetas de red**: agregar o quitar tarjetas (hasta 8; con la VM apagada) en NAT, puente, solo anfitrión, red interna o red NAT.
- **Redes virtuales**: listar las redes de VirtualBox (NAT, puente, solo anfitrión, internas, red NAT) con las VMs conectadas a cada una, crear redes internas (con o sin DHCP) y redes NAT. En el inventario, el filtro **Red** (o un clic en la red de una VM) muestra solo las VMs conectadas a esa red.
- **Configuración de red** (cambia la VM): lee la red real de la VM, detecta el gestor (ifupdown, Netplan, NetworkManager, systemd-networkd), y configura IP fija, DHCP, IP adicional (agregar o **quitar**), puerta de enlace y DNS de cada interfaz. Valida, muestra el archivo y los comandos exactos, y aplica con respaldo y **reversión automática** si algo falla.
- **Enrutamiento y diagnóstico** (no cambia las direcciones de las tarjetas), en tres secciones:
  - **Rutas**: la tabla de rutas real de la VM (`ip -4 route`) en forma de tabla (destino, puerta de enlace, interfaz, origen, métrica), con la ruta por defecto que se usa marcada; «¿por dónde sale?» (`ip route get`), y **Rutas resultantes**: si hay un cambio en preparación en Configuración de red, la tabla actual (leída de la VM) y la resultante lado a lado, con `−` lo que sale o cambia y `+` lo nuevo.
  - **Reenvío IPv4**: estado y activar/desactivar (la VM como router), persistente en `/etc/sysctl.d/`.
  - **Comandos** (si falta una herramienta, como `net-tools` en Debian 13, se instala desde el mismo panel con apt, dnf o pacman): direcciones (`ip addr`), vecinos ARP (`ip neigh`, `arp -a`), puertos en escucha (`ss -tulnp`), conexiones (`ss -tan`, `netstat -ant`), `ping`, `tracepath` y captura de paquetes (`tcpdump`). Se escoge el comando de un catálogo y se ve la línea exacta antes de ejecutarla; no se escriben comandos libres.
- Módulos heredados: comandos remotos en un equipo (SSH), llaves SSH, usuarios de Windows y configuración.

## Instalación y puesta en marcha

Guía para correr la aplicación en un equipo nuevo. Todo se hace en **Windows**, en una terminal de **PowerShell** abierta en la carpeta del proyecto.

### 1. Requisitos

| Qué | Versión | Para qué |
|---|---|---|
| Windows 10 u 11 con la virtualización activa en la BIOS (Intel VT-x o AMD-V) | — | ejecutar las VMs |
| [Oracle VirtualBox](https://www.virtualbox.org/wiki/Downloads) | 7.x (probado con 7.2.16) | el hipervisor; trae `VBoxManage.exe` y el ISO de Guest Additions |
| [Go](https://go.dev/dl/) | 1.22 o superior | compilar y ejecutar la app |
| Conexión a Internet | — | en su primer arranque, las VMs descargan paquetes y las Guest Additions |
| Espacio en disco | unos 10 GB por VM | las plantillas y los discos de las VMs |

Comprobar que todo quedó instalado:

```powershell
go version
& "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe" --version
```

### 2. Obtener el código

Con Git:

```powershell
git clone https://github.com/SalomePerez/ejecutor-ssh.git
cd ejecutor-ssh
```

o sin Git: en https://github.com/SalomePerez/ejecutor-ssh, botón **Code → Download ZIP**, descomprimir y abrir PowerShell **dentro de la carpeta descomprimida** (en el ZIP se llama `ejecutor-ssh-main`). La app busca `templates/` y `static/` en la carpeta desde donde se ejecuta.

### 3. Crear `config.properties`

```powershell
Copy-Item config.properties.example config.properties
notepad config.properties
```

El archivo copiado ya trae estas líneas; lo único que hay que escribir es la **contraseña** (`vbox.guest.password`) y el **token** (`security.apiToken`). Lo mínimo debe quedar así:

```properties
# Puerto de la app (los dos valores deben tener el mismo número)
web.addr=:8081
web.port=8081

# VirtualBox en este mismo equipo
vm.provider=virtualbox
vbox.manage=C:\Program Files\Oracle\VirtualBox\VBoxManage.exe

# Usuario y contraseña que la app crea dentro de cada VM nueva y con los que entra por las Guest Additions
vbox.guest.user=vboxuser
vbox.guest.password=escriba-aqui-una-contrasena

# Vuelta atrás automática si el cambio de red no se confirma (30 a 600 segundos)
vm.network.revertSeconds=120

# Token que protege la descarga y el borrado de llaves privadas (cualquier texto largo)
security.apiToken=escriba-aqui-un-token
```

Opcionales (si no se escriben, se usan estos valores):

```properties
vbox.templates=C:\Users\<usuario>\VirtualBox VMs\plantillas
vbox.sharedDisks=C:\Users\<usuario>\VirtualBox VMs\discos-compartidos
vbox.seeds=./seeds
vbox.additionsISO=C:\Program Files\Oracle\VirtualBox\VBoxGuestAdditions.iso
```

> **Importante:** los comentarios (`#`) van **en su propia línea**. Un comentario al final de una línea (`vbox.guest.user=vboxuser  # mi usuario`) se vuelve parte del valor.
>
> Las propiedades `ssh.*`, `vm.ssh.*` y `shell.*` del ejemplo son del módulo de comandos remotos y del diseño anterior; no hace falta cambiarlas para trabajar con VirtualBox.

`config.properties` no se sube al repositorio porque tiene contraseñas. La contraseña del invitado y el token no se muestran por `GET /api/config`.

### 4. Preparar las plantillas

Una plantilla es el disco de la imagen oficial de nube de una distribución, en formato `.vdi` o `.vmdk`. Las plantillas **no vienen en el repositorio** (cada una pesa entre 1 y 3 GB): cada persona las descarga y las pone en su equipo.

**Dónde van:** en la carpeta que indica `vbox.templates`. Si no se escribe esa propiedad, la carpeta es:

```
C:\Users\<su-usuario>\VirtualBox VMs\plantillas\
```

La app no crea esa carpeta. Para crearla:

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\VirtualBox VMs\plantillas"
```

Los archivos van sueltos dentro de la carpeta, no en subcarpetas (la app no mira subcarpetas):

```
C:\Users\<su-usuario>\VirtualBox VMs\plantillas\
├── debian-13.vdi
├── ubuntu-24.04.vdi
├── fedora-45.vdi
└── arch.vdi
```

Para usar otra carpeta (por ejemplo un disco con más espacio), se escribe en `config.properties`, por ejemplo `vbox.templates=D:\plantillas`.

La app deduce la distribución por el nombre del archivo, así que use exactamente estos nombres:

| Archivo de plantilla | Imagen oficial que se descarga | Cómo convertirla |
|---|---|---|
| `debian-13.vdi` | `debian-13-generic-amd64.tar.xz` ([cloud.debian.org](https://cloud.debian.org/images/cloud/)) | `tar -xf debian-13-generic-amd64.tar.xz` y luego `VBoxManage convertfromraw disk.raw debian-13.vdi --format VDI` |
| `ubuntu-24.04.vdi` | `noble-server-cloudimg-amd64.vmdk` ([cloud-images.ubuntu.com](https://cloud-images.ubuntu.com/noble/current/)) | `VBoxManage clonemedium disk noble-server-cloudimg-amd64.vmdk ubuntu-24.04.vdi --format VDI` |
| `fedora-45.vdi` | `Fedora-Cloud-Base-Generic-45-*.x86_64.qcow2` ([fedoraproject.org/cloud](https://fedoraproject.org/cloud/download)) | `qemu-img convert -f qcow2 -O vdi <archivo>.qcow2 fedora-45.vdi` |
| `arch.vdi` | `Arch-Linux-x86_64-cloudimg.qcow2` ([geo.mirror.pkgbuild.com](https://geo.mirror.pkgbuild.com/images/latest/)) | `qemu-img convert -f qcow2 -O vdi Arch-Linux-x86_64-cloudimg.qcow2 arch.vdi` |

- Debian: usar la imagen **generic**; la *genericcloud* no trae el controlador SATA que usa VirtualBox y no arranca.
- `VBoxManage` no lee `.qcow2`; para Fedora y Arch se usa `qemu-img` (incluido en QEMU para Windows).
- Verificar siempre la suma publicada antes de convertir: `Get-FileHash <archivo> -Algorithm SHA512` (o `SHA256`) y comparar con `SHA512SUMS`, `CHECKSUM` o `.SHA256`.
- No hacen falta las cuatro: con una plantilla ya se pueden crear VMs.
- Las plantillas no se modifican: cada VM nueva trabaja sobre una copia de la plantilla.
- Para confirmar que la app las encontró, abra **Crear nueva VM** y revise que aparecen en la lista *Plantilla*.

### 5. (Opcional) Red solo anfitrión, para el canal de gestión

Solo si se quieren VMs con tarjeta de gestión (SSH). En VirtualBox: **Archivo → Herramientas → Red → Redes solo anfitrión**. Debe existir el adaptador `VirtualBox Host-Only Ethernet Adapter` (`192.168.56.1/24`) con su **servidor DHCP habilitado**. VirtualBox lo crea al instalarse; si no está, se crea con el botón *Crear*.

### 6. Ejecutar la app

```powershell
go run .
```

Cuando arranca, la terminal muestra:

```
hipervisor: virtualbox
servidor web 2.0.0 escuchando en http://localhost:8081
```

Abrir **http://localhost:8081** en el navegador. La pastilla *Hipervisor conectado* (arriba a la derecha) confirma que la app encontró `VBoxManage`. Para detener la app: `Ctrl + C` en la terminal.

También se puede compilar una vez y ejecutar el binario, siempre desde la carpeta del proyecto:

```powershell
go build -o ejecutor-ssh.exe .
.\ejecutor-ssh.exe
```

### 7. Usar otro puerto

El puerto sale de `web.addr` y `web.port`, y **los dos deben tener el mismo número**. `web.port` arma la dirección con la que las VMs nuevas piden su configuración en el primer arranque (`http://10.0.2.2:<puerto>/cloud-init/...`); si no coincide con el puerto real, la VM arranca sin usuario, sin llave y sin Guest Additions.

- **De forma permanente:** cambiar los dos valores en `config.properties` (por ejemplo `web.addr=:8091` y `web.port=8091`).
- **Solo para una ejecución**, sin tocar el archivo (las variables de entorno tienen prioridad sobre `config.properties`):

```powershell
$env:WEB_ADDR=":8091"; $env:WEB_PORT="8091"; go run .
```

y abrir **http://localhost:8091**. Así se puede tener una segunda copia de la app corriendo junto a la del puerto 8081. Las variables solo valen en esa ventana de PowerShell. La app no lee el archivo `.env`: las variables se definen en la terminal, como en el ejemplo.

### 8. Primer uso

1. **Máquinas virtuales → Máquinas → Crear nueva VM**: nombre, plantilla, tarjeta principal `nat` y, si se quiere, la tarjeta de gestión. La VM queda lista en 3 a 7 minutos: cloud-init crea el usuario de `vbox.guest.user`, instala la llave y las Guest Additions.
2. Cuando la VM muestra su IP en el inventario, **Configurar red** abre el panel para cambiar sus interfaces.
3. **Enrutamiento y diagnóstico** muestra la tabla de rutas, el reenvío IPv4 y los comandos de consulta.

Con VMs creadas a mano (no por la app), la VM debe tener las Guest Additions instaladas y un usuario con sudo cuyo nombre y contraseña sean los de `vbox.guest.user` y `vbox.guest.password`.

### 9. Problemas frecuentes

| Síntoma | Causa y solución |
|---|---|
| `listen tcp :8081: bind: …` al arrancar | El puerto ya está en uso (por ejemplo, otra copia de la app). Cerrarla o usar otro puerto (paso 7). |
| `falta vbox.guest.user en config.properties` | Llenar `vbox.guest.user` y `vbox.guest.password` (paso 3). |
| La pastilla no dice *Hipervisor conectado* | `vbox.manage` no apunta a `VBoxManage.exe`. Revisar la ruta. |
| La lista de plantillas está vacía | Los archivos no están en `vbox.templates` o no terminan en `.vdi` o `.vmdk` (paso 4). |
| La VM nueva arranca sin usuario y la app no puede leerla | `web.addr` y `web.port` no coinciden (paso 7), o la VM no tiene Internet por su tarjeta NAT. |
| `go: error obtaining buildID for go tool link: … Una directiva de Control de aplicaciones bloqueó este archivo` | **Smart App Control** de Windows bloquea el enlazador de Go (`link.exe`), que no está firmado. No es un error de la app: Go no puede compilar nada en ese equipo. Solución: *Seguridad de Windows → Control de aplicaciones y navegador → Configuración de Smart App Control → Desactivado*. En muchas versiones de Windows 11 no se puede volver a activar sin reinstalar Windows. La alternativa es correr la app en un equipo sin Smart App Control. Para saber si está activo: `Get-MpComputerStatus \| Select-Object SmartAppControlState`. |
| Windows bloquea `ejecutor-ssh.exe` o los binarios de prueba | Es la misma causa: Smart App Control bloquea ejecutables recién compilados que no están firmados. |
| `arp` o `netstat` «no está instalado» en una VM | Normal en Debian 13 y Fedora: el panel ofrece instalar `net-tools` con un botón (la VM necesita Internet). |

### Pruebas

```powershell
go vet ./...
go test ./...
# contra VirtualBox real (solo lectura salvo GuestExec):
$env:VBOX_LIVE=1; go test ./mv -run Live -v
```

## Mecanismos de acceso a una VM

Una VM se puede alcanzar por dos mecanismos **independientes**:

| Mecanismo | Cómo se entra | Depende de la red de la VM |
|---|---|---|
| **Hipervisor** | la consola de VirtualBox (botón *Abrir consola* de cada VM) y, para la app, las Guest Additions (`guestcontrol`) | no |
| **Canal adicional: SSH** | SSH desde el anfitrión a la tarjeta de gestión (*solo anfitrión*); el comando aparece en cada VM | sí, solo de la tarjeta de gestión |

El acceso por el hipervisor es el normal: siempre está disponible y no depende de ninguna IP. El SSH de gestión **no es un paso obligatorio**: se implementó para demostrar que una VM puede tener un segundo mecanismo de acceso y administración, independiente del primero. Por ejemplo, con la consola de VirtualBox abierta se puede entrar al mismo tiempo por SSH desde el anfitrión, y los dos funcionan a la vez. La tarjeta de gestión es opcional al crear la VM.

La app abre la consola sobre la VM en marcha (`VBoxManage startvm <vm> --type separate`), sin reiniciarla. Para cerrar la ventana sin apagar la VM, escoja *Continuar ejecutando en segundo plano*.

## Canales de administración de la app

Cambiar la IP de una VM por SSH a esa misma IP corta la sesión. La app usa canales que no pasan por la interfaz que se modifica:

| Canal | Cómo entra a la VM | Requisito | Estado |
|---|---|---|---|
| En banda | SSH a la interfaz que se cambia | — | descartado: se corta |
| Canal interno (hipervisor) | `VBoxManage guestcontrol run` (Guest Additions, dispositivo VMMDev) | Guest Additions y usuario con contraseña en la VM | **implementado** (por defecto) |
| Canal de gestión (SSH) | SSH a una segunda tarjeta en la red *solo anfitrión* | tarjeta de gestión con IP (DHCP de VirtualBox) | **implementado** (opcional) |

- El canal interno (Guest Additions) es el equivalente en VirtualBox de `qemu-guest-agent` (el canal del diseño anterior con libvirt).
- En el canal de gestión (SSH) la IP de gestión se obtiene del servidor DHCP de VirtualBox (`dhcpserver findlease`), sin depender de las Guest Additions; con ese canal el plan **no permite modificar la interfaz de gestión**. Funciona igual con un equipo físico que tenga una interfaz de gestión.
- En los paneles de Configuración de red y de Enrutamiento y diagnóstico se elige el canal; en la API es el parámetro `?canal=gestion`.

## Crear una VM

1. La plantilla (`.vdi`) se copia y se agranda al tamaño pedido.
2. La primera tarjeta es **NAT**: por ella la VM pide su configuración a la app.
3. cloud-init encuentra su configuración por el número de serie DMI: `ds=nocloud;s=http://10.0.2.2:<web.port>/cloud-init/<vm>/` (con la configuración por defecto, puerto 8081). Desde la NAT, `10.0.2.2` es este equipo; la app sirve `user-data` y `meta-data` solo a conexiones locales.
   - VirtualBox 7 exige `--nat-localhostreachable1 on` para que la VM llegue al anfitrión por `10.0.2.2`; la app lo activa al crear.
4. cloud-init crea el usuario de la app (`vbox.guest.user`, con contraseña y la llave `keys/<vm>-ssh`) e instala las Guest Additions desde el ISO de VirtualBox.
5. Opcional: **interfaz de gestión** (canal de gestión), una tarjeta *solo anfitrión* que cloud-init deja en DHCP sin ruta por defecto.

La VM queda lista en 3 a 7 minutos (actualiza paquetes antes de instalar las Guest Additions).

### Plantillas

Cómo prepararlas: ver [Instalación, paso 4](#4-preparar-las-plantillas).

## Escenario de prueba: VM como router

```
lab-debian (A, Netplan)          debian-red (R, ifupdown)            lab-ubuntu (B, Netplan)
192.168.10.10 ── interna:lan-a ── 192.168.10.1 | 192.168.20.1 ── interna:lan-b ── 192.168.20.10
```

1. Redes virtuales → crear las redes internas `lan-a` (192.168.10.0/24) y `lan-b` (192.168.20.0/24), sin DHCP.
2. Tarjetas → A: `interna:lan-a`; B: `interna:lan-b`; R: las dos.
3. Configuración de red → R: IP fija en cada tarjeta (`192.168.10.1/24` y `192.168.20.1/24`, sin puerta de enlace). Enrutamiento y diagnóstico → R → Reenvío IPv4: **Activar**.
4. Configuración de red → A: `192.168.10.10/24`, puerta de enlace `192.168.10.1`, uso *solo hacia ciertas redes* `192.168.20.0/24`. B igual, en espejo.
5. Enrutamiento y diagnóstico → A: en Rutas, «¿por dónde sale?» hacia `192.168.20.10` debe dar `via 192.168.10.1`; en Comandos, Ping. Con el reenvío de R desactivado, el ping falla.

## API principal

| Método y ruta | Qué hace |
|---|---|
| `GET /api/vms` | VMs con estado, recursos, IP (Guest Additions), IP de gestión (`mgmtIp`) y tarjetas |
| `POST /api/vms` | crear: `{"name","distro","cpu","memoryMB","diskGB","imagePath","networks":["nat","interna:lan-a"],"management":true,"disks":[...]}` |
| `POST /api/vms/{vm}/start` · `/stop` · `/rename` · `PUT` · `DELETE` | ciclo de vida |
| `POST /api/vms/{vm}/console` | abre la consola de VirtualBox de la VM encendida, sin reiniciarla |
| `GET /api/templates` | plantillas disponibles |
| `GET /api/hypervisor` | memoria y CPU del anfitrión |
| `GET/POST /api/networks`, `DELETE /api/networks/{red}` | redes de VirtualBox |
| `POST /api/vms/{vm}/interfaces` `{"network"}` · `DELETE …/interfaces/{mac}` | tarjetas (VM apagada) |
| `GET /api/vms/{vm}/network` | leer la red de la VM |
| `POST /api/vms/{vm}/network/plan` | validar y mostrar archivos y comandos (no ejecuta) |
| `POST /api/vms/{vm}/network/apply` | aplicar con respaldo, verificación y reversión |
| `POST /api/vms/{vm}/network/forwarding` `{"enabled":true}` | reenvío IPv4 persistente |
| `POST /api/vms/{vm}/network/test` `{"target":"192.168.20.10"}` | ping, `ip route get`, `tracepath` |
| `GET /api/vms/{vm}/network/routing` | tabla de rutas interpretada (`routes`, `summary`, `warning`) y estado del reenvío (`forwarding`) |
| `GET /api/netcmds` | catálogo de comandos de red y filtros de captura |
| `POST /api/vms/{vm}/network/cmd` `{"cmd":"route"}` | ejecuta un comando del catálogo: `addr`, `route`, `neigh`, `arp`, `ss`, `conns`, `netstat`, `routeget`, `ping` y `tracepath` (con `target`), `tcpdump` (con `iface`, `count` 1-50, `filter`) |

Las rutas `/network*` aceptan `?canal=gestion` para usar el canal de gestión.

Cuerpo de `plan` y `apply`: `{"interface":"enp0s8","mode":"static","ip":"192.168.10.10","mask":"/24","gateway":"192.168.10.1","role":"routes","nets":"192.168.20.0/24","dns":""}`. `mode`: `dhcp`, `static`, `extra` (agrega `ip`) o `remove` (quita la IP adicional `ip`; lo demás no cambia); `role`: `main`, `backup`, `routes` o `none`.

## Estructura

```
main.go                 rutas HTTP y elección del proveedor (virtualbox | libvirt)
config/                 config.properties y variables de entorno
handlers/               API REST (VMs, red, redes, llaves, usuarios, cloud-init)
mv/
  provider.go           interfaz Provider (crear, listar, encender…)
  vbox*.go              proveedor VirtualBox (VBoxManage, guestcontrol, redes, creación)
  mgmt_channel.go       canal de gestión: SSH por la interfaz de gestión
  network*.go           detección, plan por gestor de red, aplicación con reversión,
                        reenvío IPv4 y prueba de conectividad (comunes a los proveedores)
  network_cmds.go       catálogo de comandos de consulta y diagnóstico
  network_routing.go    tabla de rutas interpretada y estado del reenvío
  libvirt*.go, guestagent.go   proveedor anterior (libvirt + qemu-guest-agent)
static/, templates/     interfaz web (network.js: Configuración de red; netcmds.js: Enrutamiento y diagnóstico)
```

## Seguridad

- `VBoxManage` se ejecuta con argumentos separados, sin shell.
- Los scripts dentro de la VM se pasan como argumento (`sh -c "$1"`) y la contraseña de `sudo` por variable de entorno (canal interno) o entrada estándar (canal de gestión), nunca en la línea de comandos de la VM.
- `/cloud-init/` solo responde a conexiones locales (la NAT de la VM llega como 127.0.0.1).
- El canal de gestión guarda la llave de host de cada VM la primera vez (`keys/known_hosts_vms`) y después exige que coincida.
- Descarga y borrado de llaves privadas exigen `X-API-Key`.