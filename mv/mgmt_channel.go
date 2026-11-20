package vm

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ---------- Canal de gestion: SSH a la tarjeta de gestion ----------
// La VM tiene una segunda tarjeta reservada para la gestion, en la red
// "solo anfitrion" de VirtualBox (192.168.56.0/24). La app entra por SSH a
// la IP de esa tarjeta y desde ahi configura las DEMAS interfaces (las de
// datos). Como la tarjeta de gestion nunca se modifica, el canal no se corta
// aunque cambie la red de datos. A diferencia del canal interno (Guest Additions),
// no depende del hipervisor: funciona igual con un equipo fisico que tenga
// una interfaz de gestion.
// La IP de gestion se lee del servidor DHCP de VirtualBox (findlease), no de
// las Guest Additions, para que este canal no dependa del canal interno.

// MgmtTarget es la tarjeta de gestion de una VM.
type MgmtTarget struct {
	IP      string
	MAC     string
	Network string
}

// NetworkOps son las operaciones del panel de red por un canal concreto.
type NetworkOps interface {
	ReadNetwork(name string) (*NetState, error)
	ApplyNetwork(name string, req NetRequest, revertSeconds int) (*ApplyResult, error)
	SetForwarding(name string, enabled bool) (*ApplyResult, error)
	TestConnectivity(name, target string) (*ApplyResult, error)
}

var _ NetworkOps = (*VBoxProvider)(nil)

var reLeaseIP = regexp.MustCompile(`(?m)^IP Address:\s+([\d.]+)`)

// ManagementTarget busca la tarjeta en "solo anfitrion" y su IP en los
// leases del DHCP de VirtualBox.
func (p *VBoxProvider) ManagementTarget(name string) (*MgmtTarget, error) {
	values, err := p.info(name)
	if err != nil {
		return nil, err
	}
	for n := 1; n <= 8; n++ {
		if values[fmt.Sprintf("nic%d", n)] != "hostonly" {
			continue
		}
		adapter := values[fmt.Sprintf("hostonlyadapter%d", n)]
		mac := formatMAC(values[fmt.Sprintf("macaddress%d", n)])
		t := &MgmtTarget{MAC: mac, Network: "solo-anfitrion:" + adapter}
		out, err := p.vbox.Run("dhcpserver", "findlease", "--interface="+adapter, "--mac-address="+mac)
		if err != nil {
			return t, fmt.Errorf("%s tiene la tarjeta de gestión %s, pero el DHCP de VirtualBox no le ha prestado IP: ¿está configurada en DHCP dentro de la VM?", name, mac)
		}
		m := reLeaseIP.FindStringSubmatch(string(out))
		if m == nil {
			return t, fmt.Errorf("no se entendió el lease de %s: %s", mac, strings.TrimSpace(string(out)))
		}
		t.IP = m[1]
		return t, nil
	}
	return nil, fmt.Errorf("%s no tiene interfaz de gestión: agréguele una tarjeta en la red «solo anfitrión» (botón Tarjetas)", name)
}

// mgmtBackend ejecuta los scripts del panel de red por SSH a la interfaz de
// gestion. Lo demas (redes del hipervisor, nombre) lo toma de VirtualBox.
type mgmtBackend struct {
	*VBoxProvider
	target *MgmtTarget
	user   string
	pass   string
	key    string
	// client es la conexion SSH, que se reutiliza durante toda la operacion:
	// abrir una por comando hace que sshd (PerSourcePenalties, MaxStartups)
	// empiece a rechazar conexiones a mitad de un cambio de red.
	mu     sync.Mutex
	client *ssh.Client
}

func (b *mgmtBackend) hypervisorName() string { return "VirtualBox" }

// conn devuelve la conexion abierta o abre una nueva.
func (b *mgmtBackend) conn() (*ssh.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		return b.client, nil
	}
	c, err := b.dial()
	if err != nil {
		return nil, err
	}
	b.client = c
	return c, nil
}

// Close cierra la conexion (al terminar la operacion o para reconectar).
func (b *mgmtBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		_ = b.client.Close()
		b.client = nil
	}
}

// GuestExec por SSH. El script corre como root: con sudo -S, que lee la
// contrasena de la primera linea de la entrada estandar. Si la conexion se
// cayo entre dos comandos, reconecta una vez.
func (b *mgmtBackend) GuestExec(name, script string, timeout time.Duration) (*GuestExecResult, error) {
	res, err := b.exec(name, script, timeout)
	if err != nil {
		b.Close()
		res, err = b.exec(name, script, timeout)
	}
	return res, err
}

func (b *mgmtBackend) exec(name, script string, timeout time.Duration) (*GuestExecResult, error) {
	client, err := b.conn()
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("abrir sesión SSH con %s: %w", b.target.IP, err)
	}
	defer session.Close()
	// stdout y stderr se copian en goroutines distintas: el buffer debe
	// estar protegido (bytes.Buffer no es seguro para escrituras paralelas).
	output := &lockedBuffer{}
	session.Stdout, session.Stderr = output, output
	session.Stdin = strings.NewReader(b.pass + "\n")
	command := "sudo -S -p '' sh -c " + quoteShell(guestPath+script) + " 2>&1; echo \"" + exitMarker + "$?\""
	if b.user == "root" {
		command = "sh -c " + quoteShell(guestPath+script) + " 2>&1; echo \"" + exitMarker + "$?\""
	}
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(timeout):
		return nil, fmt.Errorf("el script en %s (SSH %s) no terminó en %s", name, b.target.IP, timeout)
	}
	text := strings.ReplaceAll(output.String(), "\r\n", "\n")
	m := reExitMarker.FindStringSubmatchIndex(text)
	if m == nil {
		log.Printf("canal de gestión %s: sin marca de salida (err=%v)\n  comando: %s\n  salida: %q", name, runErr, command, text)
		return nil, fmt.Errorf("el script en %s no terminó por SSH (%v): %s", name, runErr, strings.TrimSpace(text))
	}
	code := 0
	fmt.Sscanf(text[m[2]:m[3]], "%d", &code)
	return &GuestExecResult{ExitCode: code, Stdout: text[:m[0]]}, nil
}

// knownHostsMu protege el archivo de llaves de host conocidas.
var knownHostsMu sync.Mutex

// dial abre la conexion SSH. La llave de host se acepta la primera vez y se
// guarda (TOFU) en keys/known_hosts_vms; despues debe coincidir.
func (b *mgmtBackend) dial() (*ssh.Client, error) {
	auths := []ssh.AuthMethod{}
	if pem, err := os.ReadFile(b.key); err == nil {
		if signer, err := ssh.ParsePrivateKey(pem); err == nil {
			auths = append(auths, ssh.PublicKeys(signer))
		}
	}
	if b.pass != "" {
		auths = append(auths, ssh.Password(b.pass))
	}
	knownFile := filepath.Join(filepath.Dir(b.key), "known_hosts_vms")
	config := &ssh.ClientConfig{
		User:            b.user,
		Auth:            auths,
		HostKeyCallback: tofuHostKey(knownFile),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(b.target.IP, "22"), config)
	if err != nil {
		return nil, fmt.Errorf("SSH a la interfaz de gestión %s (%s): %w", b.target.IP, b.target.MAC, err)
	}
	return client, nil
}

func tofuHostKey(file string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		if _, err := os.Stat(file); err == nil {
			check, err := knownhosts.New(file)
			if err != nil {
				return err
			}
			err = check(hostname, remote, key)
			var keyErr *knownhosts.KeyError
			if err == nil || !errors.As(err, &keyErr) || len(keyErr.Want) > 0 {
				// nil: conocida; Want con datos: la llave cambio (posible MITM
				// o VM recreada con la misma IP: borre la linea del archivo).
				return err
			}
		}
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
		return err
	}
}

// mgmtOps son las operaciones del panel de red por el canal de gestion.
type mgmtOps struct{ p *VBoxProvider }

// Management devuelve las operaciones de red por la interfaz de gestion.
func (p *VBoxProvider) Management() NetworkOps { return mgmtOps{p} }

func (o mgmtOps) backend(name string) (*mgmtBackend, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	target, err := o.p.ManagementTarget(name)
	if err != nil {
		return nil, err
	}
	creds := GuestCredentials{}
	if o.p.credentials != nil {
		creds, _ = o.p.credentials(name)
	}
	if creds.User == "" {
		return nil, fmt.Errorf("configure vbox.guest.user: es el usuario con el que la app entra por SSH")
	}
	return &mgmtBackend{VBoxProvider: o.p, target: target, user: creds.User, pass: creds.Password,
		key: filepath.Join(o.p.opts.KeysDir, name+"-ssh")}, nil
}

// markManagement anota en la lectura cual es la interfaz de gestion y por
// que canal se leyo, para que el plan no la toque.
func markManagement(st *NetState, b *mgmtBackend) {
	st.Management = b.target.MAC
	st.Channel = "el canal de gestión (SSH a " + b.target.IP + ")"
}

func (o mgmtOps) ReadNetwork(name string) (*NetState, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	st, err := readNetwork(b, name)
	if err != nil {
		return nil, err
	}
	markManagement(st, b)
	return st, nil
}

func (o mgmtOps) ApplyNetwork(name string, req NetRequest, revertSeconds int) (*ApplyResult, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	return applyNetworkWith(b, name, req, revertSeconds, func(st *NetState) { markManagement(st, b) })
}

func (o mgmtOps) SetForwarding(name string, enabled bool) (*ApplyResult, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	return setForwarding(b, name, enabled)
}

func (o mgmtOps) TestConnectivity(name, target string) (*ApplyResult, error) {
	b, err := o.backend(name)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	return testConnectivity(b, name, target)
}

// lockedBuffer es un bytes.Buffer seguro para escrituras concurrentes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
