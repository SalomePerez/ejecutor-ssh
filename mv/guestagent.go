package vm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GuestExecResult es el resultado de un script ejecutado DENTRO de la VM.
type GuestExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Output junta stdout y stderr, como CombinedOutput de SSH.
func (r *GuestExecResult) Output() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return strings.TrimRight(r.Stdout, "\n") + "\n" + r.Stderr
}

// guestPollInterval es cada cuanto se pregunta al agente si el script termino.
var guestPollInterval = 300 * time.Millisecond

// guestPath fija un PATH y un idioma conocidos: qemu-ga arranca los procesos
// con un entorno minimo, y con LC_ALL=C la salida de ip/nmcli/systemctl
// siempre llega en ingles, que es lo que entiende el parser.
const guestPath = "export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C; "

// GuestExec ejecuta un script de shell dentro de la VM a traves de
// qemu-guest-agent (guest-exec + guest-exec-status). El agente habla con el
// hipervisor por un canal virtio interno, NO por la red de la VM: por eso es
// el canal que usa el panel de red, que no se corta aunque cambie la IP.
// Requiere que la VM este encendida y que el agente permita guest-exec
// (cloud-init lo instala en las VMs que crea la aplicacion).
func (p *LibvirtProvider) GuestExec(name, script string, timeout time.Duration) (*GuestExecResult, error) {
	if p == nil || p.sshClient == nil {
		return nil, fmt.Errorf("cliente SSH no inicializado")
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	request, _ := json.Marshal(map[string]interface{}{
		"execute": "guest-exec",
		"arguments": map[string]interface{}{
			"path":           "/bin/sh",
			"arg":            []string{"-c", guestPath + script},
			"capture-output": true,
		},
	})
	output, err := p.sshClient.ExecuteCommand(agentCommand(name, string(request)))
	if err != nil {
		details := strings.TrimSpace(string(output))
		return nil, fmt.Errorf("el agente QEMU de %s no respondió (¿la VM está encendida y tiene qemu-guest-agent?): %w: %s", name, err, details)
	}
	var started struct {
		Return struct {
			PID int `json:"pid"`
		} `json:"return"`
	}
	if err := json.Unmarshal(output, &started); err != nil || started.Return.PID == 0 {
		return nil, fmt.Errorf("respuesta inesperada de guest-exec en %s: %s", name, strings.TrimSpace(string(output)))
	}

	statusRequest := fmt.Sprintf(`{"execute":"guest-exec-status","arguments":{"pid":%d}}`, started.Return.PID)
	deadline := time.Now().Add(timeout)
	for {
		output, err := p.sshClient.ExecuteCommand(agentCommand(name, statusRequest))
		if err != nil {
			return nil, fmt.Errorf("consultar guest-exec-status en %s: %w", name, err)
		}
		var status struct {
			Return struct {
				Exited   bool   `json:"exited"`
				ExitCode int    `json:"exitcode"`
				OutData  string `json:"out-data"`
				ErrData  string `json:"err-data"`
			} `json:"return"`
		}
		if err := json.Unmarshal(output, &status); err != nil {
			return nil, fmt.Errorf("respuesta inesperada de guest-exec-status en %s: %s", name, strings.TrimSpace(string(output)))
		}
		if status.Return.Exited {
			stdout, _ := base64.StdEncoding.DecodeString(status.Return.OutData)
			stderr, _ := base64.StdEncoding.DecodeString(status.Return.ErrData)
			return &GuestExecResult{ExitCode: status.Return.ExitCode, Stdout: string(stdout), Stderr: string(stderr)}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("el script en %s no terminó en %s", name, timeout)
		}
		p.wait(guestPollInterval)
	}
}

func agentCommand(name, payload string) string {
	return "sudo -n virsh --connect qemu:///system qemu-agent-command " + quoteShell(name) + " " + quoteShell(payload)
}

// wait permite que las pruebas no esperen de verdad.
func (p *LibvirtProvider) wait(d time.Duration) {
	if p.sleep != nil {
		p.sleep(d)
		return
	}
	time.Sleep(d)
}
