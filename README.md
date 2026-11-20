# Ejecutor SSH

Aplicación web en Go que **crea máquinas virtuales y automatiza la configuración de su red** (IP fija, DHCP, IP adicional, rutas, reenvío IPv4) en distribuciones Linux de distintas familias, de forma remota y sin cortar la comunicación al cambiar la IP.

El hipervisor es **VirtualBox**, en el mismo equipo que la aplicación. La app lo controla con `VBoxManage`; no hay virtualización anidada.

```
Equipo físico (Windows)
├── Navegador ──HTTP──► App web (Go, :8081)
│                          │ VBoxManage (local)
│                          ▼
└── VirtualBox ──► VM Debian · VM Ubuntu · VM Fedora · VM Arch …
                    (NAT, puente, solo anfitrión, redes internas)
```

## Qué hace

- **Máquinas virtuales**: crear desde plantillas (imágenes oficiales de nube con cloud-init), encender, apagar, editar CPU y memoria, renombrar, eliminar, discos adicionales y compartidos.
- **Tarjetas de red**: agregar o quitar tarjetas (hasta 8; con la VM apagada) en NAT, puente, solo anfitrión, red interna o red NAT.
- **Redes virtuales**: listar las redes de VirtualBox (NAT, puente, solo anfitrión, internas, red NAT) con las VMs conectadas a cada una, crear redes internas (con o sin DHCP) y redes NAT, y crear de un clic las del laboratorio (**LAN A** `interna:lan-a` 192.168.10.0/24 y **LAN B** `interna:lan-b` 192.168.20.0/24). En el inventario, el filtro **Red** (o un clic en la red de una VM) muestra solo las VMs conectadas a esa red.
- **Configuración de red** (cambia la VM): lee la red real de la VM, detecta el gestor (ifupdown, Netplan, NetworkManager, systemd-networkd), y configura IP fija, DHCP o IP adicional, puerta de enlace y DNS de cada interfaz. Valida, muestra el archivo, los comandos y las rutas resultantes, y aplica con respaldo y **reversión automática** si algo falla. Incluye el **reenvío IPv4** (la VM como router).
- **Comandos de red** (solo consulta y diagnóstico, no cambia nada): tabla de rutas (`ip route`), direcciones (`ip addr`), vecinos ARP (`ip neigh`, `arp -a`), puertos en escucha (`ss -tulnp`), conexiones (`ss -tan`, `netstat -ant`), `ping`, `tracepath` y captura de paquetes (`tcpdump`). Se escoge el comando de un catálogo; no se escriben comandos libres.
- Módulos heredados: comandos remotos en un equipo (SSH), llaves SSH, usuarios de Windows y configuración.

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
- En los paneles de Configuración de red y Comandos de red se elige el canal; en la API es el parámetro `?canal=gestion`.

## Crear una VM

1. La plantilla (`.vdi`) se copia y se agranda al tamaño pedido.
2. La primera tarjeta es **NAT**: por ella la VM pide su configuración a la app.
3. cloud-init encuentra su configuración por el número de serie DMI: `ds=nocloud;s=http://10.0.2.2:8081/cloud-init/<vm>/`. Desde la NAT, `10.0.2.2` es este equipo; la app sirve `user-data` y `meta-data` solo a conexiones locales.
   - VirtualBox 7 exige `--nat-localhostreachable1 on` para que la VM llegue al anfitrión por `10.0.2.2`; la app lo activa al crear.
4. cloud-init crea el usuario de la app (`vbox.guest.user`, con contraseña y la llave `keys/<vm>-ssh`) e instala las Guest Additions desde el ISO de VirtualBox.
5. Opcional: **interfaz de gestión** (canal de gestión), una tarjeta *solo anfitrión* que cloud-init deja en DHCP sin ruta por defecto.

La VM queda lista en 3 a 7 minutos (actualiza paquetes antes de instalar las Guest Additions).

### Plantillas

Carpeta `vbox.templates` (por defecto `%USERPROFILE%\VirtualBox VMs\plantillas`). Cada archivo `.vdi` o `.vmdk` es una plantilla; la distribución se deduce del nombre.

| Plantilla | Imagen oficial | Conversión |
|---|---|---|
| `debian-13.vdi` | `debian-13-generic-amd64.tar.xz` (cloud.debian.org) | `tar -xf` → `VBoxManage convertfromraw disk.raw debian-13.vdi --format VDI` |
| `ubuntu-24.04.vdi` | `noble-server-cloudimg-amd64.vmdk` (cloud-images.ubuntu.com) | `VBoxManage clonemedium disk <vmdk> ubuntu-24.04.vdi --format VDI` |

Usar la imagen **generic** de Debian (la *genericcloud* no trae el controlador SATA que usa VirtualBox). Verificar siempre las sumas publicadas (SHA512SUMS / SHA256SUMS).

## Escenario de prueba: VM como router

```
lab-debian (A, Netplan)          debian-red (R, ifupdown)            lab-ubuntu (B, Netplan)
192.168.10.10 ── interna:lan-a ── 192.168.10.1 | 192.168.20.1 ── interna:lan-b ── 192.168.20.10
```

1. Redes virtuales → **Crear LAN A y LAN B** (`lan-a` 192.168.10.0/24 y `lan-b` 192.168.20.0/24, sin DHCP).
2. Tarjetas → A: `interna:lan-a`; B: `interna:lan-b`; R: las dos.
3. Red → R: IP fija en cada tarjeta (sin puerta de enlace) y **Activar reenvío**.
4. Red → A: `192.168.10.10/24`, puerta de enlace `192.168.10.1`, uso *solo hacia ciertas redes* `192.168.20.0/24`. B igual, en espejo.
5. Probar conectividad desde A hacia `192.168.20.10`. Con el reenvío de R desactivado, el ping falla.

## Configuración

`config.properties` (copiar de `config.properties.example`; no se sube al repositorio):

```properties
web.addr=:8081
web.port=8081

vm.provider=virtualbox                      # o libvirt (diseño anterior, por SSH)
vbox.manage=C:\Program Files\Oracle\VirtualBox\VBoxManage.exe
vbox.guest.user=vboxuser                    # usuario que la app crea y usa dentro de las VMs
vbox.guest.password=...                     # su contraseña (guestcontrol y sudo)
# Opcionales:
# vbox.templates=C:\Users\<usuario>\VirtualBox VMs\plantillas
# vbox.sharedDisks=C:\Users\<usuario>\VirtualBox VMs\discos-compartidos
# vbox.seeds=./seeds
# vbox.seedURL=http://10.0.2.2:8081
# vbox.additionsISO=C:\Program Files\Oracle\VirtualBox\VBoxGuestAdditions.iso

vm.network.revertSeconds=120                # vuelta atrás automática si no se confirma (30-600 s)
keys.storePath=./keys
security.apiToken=...                       # protege la descarga y el borrado de llaves privadas
```

La contraseña del invitado y el token no se exponen por `GET /api/config`.

## Ejecutar

Requisitos: Go 1.22+, VirtualBox 7.x con su ISO de Guest Additions.

```powershell
go run .
```

Abrir `http://localhost:8081`.

En este equipo, Smart App Control de Windows bloquea a veces los ejecutables recién compilados (`ejecutor-ssh.exe`, binarios de prueba). `go run .` suele funcionar; si se bloquea, volver a intentar o firmar el binario.

Pruebas:

```powershell
go vet ./...
go test ./...
# contra VirtualBox real (solo lectura salvo GuestExec):
$env:VBOX_LIVE=1; go test ./mv -run Live -v
```

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
| `GET /api/netcmds` | catálogo de comandos de red y filtros de captura |
| `POST /api/vms/{vm}/network/cmd` `{"cmd":"route"}` | ejecuta un comando del catálogo: `addr`, `route`, `neigh`, `arp`, `ss`, `conns`, `netstat`, `ping` y `tracepath` (con `target`), `tcpdump` (con `iface`, `count` 1-50, `filter`) |

Las rutas `/network*` aceptan `?canal=gestion` para usar el canal de gestión.

Cuerpo de `plan` y `apply`: `{"interface":"enp0s8","mode":"static","ip":"192.168.10.10","mask":"/24","gateway":"192.168.10.1","role":"routes","nets":"192.168.20.0/24","dns":""}`. `mode`: `dhcp`, `static` o `extra`; `role`: `main`, `backup`, `routes` o `none`.

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
  network_cmds.go       catálogo de Comandos de red (consulta y diagnóstico)
  libvirt*.go, guestagent.go   proveedor anterior (libvirt + qemu-guest-agent)
static/, templates/     interfaz web (network.js: Configuración de red; netcmds.js: Comandos de red)
```

## Seguridad

- `VBoxManage` se ejecuta con argumentos separados, sin shell.
- Los scripts dentro de la VM se pasan como argumento (`sh -c "$1"`) y la contraseña de `sudo` por variable de entorno (canal interno) o entrada estándar (canal de gestión), nunca en la línea de comandos de la VM.
- `/cloud-init/` solo responde a conexiones locales (la NAT de la VM llega como 127.0.0.1).
- El canal de gestión guarda la llave de host de cada VM la primera vez (`keys/known_hosts_vms`) y después exige que coincida.
- Descarga y borrado de llaves privadas exigen `X-API-Key`.

## Licencia

Trabajo de grado. Consulte los términos de uso de la institución.
