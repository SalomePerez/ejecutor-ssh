package vm

import (
	"fmt"
	"strings"
	"time"
)

// ApplyLine es un comando ejecutado y lo que respondio.
type ApplyLine struct {
	Cmd    string `json:"cmd,omitempty"`
	Output string `json:"output,omitempty"`
	Note   string `json:"note,omitempty"`
	Host   bool   `json:"host,omitempty"`
	Error  bool   `json:"error,omitempty"`
	Warn   bool   `json:"warn,omitempty"`
}

// ApplyStep es una fase con su estado: ok, bad o skip.
type ApplyStep struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Lines  []ApplyLine `json:"lines"`
}

// ApplyResult es lo que vuelve al panel despues de aplicar.
type ApplyResult struct {
	OK       bool        `json:"ok"`
	Reverted bool        `json:"reverted"`
	Message  string      `json:"message"`
	Steps    []ApplyStep `json:"steps"`
	State    *NetState   `json:"state,omitempty"`
	// Install, en Comandos de red: la herramienta que falta y que la app
	// puede instalar (arp, netstat, tcpdump…).
	Install string `json:"install,omitempty"`
}

// PlanError se devuelve cuando la solicitud no pasa la validacion.
type PlanError struct{ Plan *NetPlan }

func (e *PlanError) Error() string {
	for _, c := range e.Plan.Checks {
		if c.Level == "bad" {
			return c.Text
		}
	}
	if !e.Plan.Changed {
		return "no hay nada que cambiar"
	}
	return "la configuración no es válida"
}

const guestStepTimeout = 90 * time.Second

// ApplyNetwork vuelve a leer la VM (nunca confia en una lectura vieja),
// valida, y ejecuta el plan por fases: comprobar, respaldar (con la vuelta
// atras programada dentro de la VM), escribir, aplicar, verificar y cerrar.
// Si algo falla despues del respaldo, dispara la reversion de inmediato.
func (p *LibvirtProvider) ApplyNetwork(name string, req NetRequest, revertSeconds int) (*ApplyResult, error) {
	return applyNetwork(p, name, req, revertSeconds)
}

// applyNetwork es comun a todos los proveedores (ver netBackend).
func applyNetwork(p netBackend, name string, req NetRequest, revertSeconds int) (*ApplyResult, error) {
	return applyNetworkWith(p, name, req, revertSeconds, nil)
}

// applyNetworkWith permite al canal completar cada lectura (por ejemplo, el
// canal de gestion marca su interfaz para que el plan no la toque).
func applyNetworkWith(p netBackend, name string, req NetRequest, revertSeconds int, mark func(*NetState)) (*ApplyResult, error) {
	st, err := readNetwork(p, name)
	if err != nil {
		return nil, err
	}
	if mark != nil {
		mark(st)
	}
	plan := PlanNetwork(st, req, revertSeconds)
	if !plan.Valid {
		return nil, &PlanError{Plan: plan}
	}

	result := &ApplyResult{}
	armed := false // ya se programo la reversion dentro de la VM
	failedAt := -1

	for n, phase := range plan.Phases {
		step := ApplyStep{Name: phase.Name, Status: "ok"}
		failed := false
		for _, c := range phase.Cmds {
			if c.Cmd == "" && c.run == "" {
				step.Lines = append(step.Lines, ApplyLine{Note: c.Note})
				continue
			}
			line, ok := runPlanCmd(p, name, c)
			step.Lines = append(step.Lines, line)
			if !ok && !c.soft && !c.ignoreErr {
				failed = true
				break
			}
		}
		if phase.kind == "backup" && !failed {
			armed = true
		}
		if failed {
			step.Status = "bad"
			result.Steps = append(result.Steps, step)
			failedAt = n
			break
		}
		result.Steps = append(result.Steps, step)
	}

	if failedAt >= 0 {
		for k := failedAt + 1; k < len(plan.Phases); k++ {
			result.Steps = append(result.Steps, ApplyStep{Name: plan.Phases[k].Name, Status: "skip"})
		}
		failed := plan.Phases[failedAt]
		switch {
		case failed.kind == "check":
			result.Message = "No se aplicó: la IP ya responde en la red (otro equipo la tiene). No se tocó nada."
		case armed:
			result.Reverted = true
			rev := ApplyStep{Name: "Revertir", Status: "ok"}
			u := plan.revertUnit
			// El servicio corre en segundo plano: se espera a que termine para
			// que la lectura final muestre la configuracion ya restaurada.
			line, ok := runPlanCmd(p, name, NetCmd{Cmd: "systemctl start " + u + ".service", Note: "la app no espera los " + fmt.Sprint(plan.RevertSeconds) + " s: dispara la reversión ya",
				run: fmt.Sprintf("systemctl start %s.service || exit 1; systemctl stop %s.timer 2>/dev/null; for k in $(seq 1 60); do systemctl is-active --quiet %s.service || break; sleep 1; done; true", u, u, u), mustOK: true})
			rev.Lines = append(rev.Lines, line)
			if !ok {
				rev.Status = "bad"
			}
			result.Steps = append(result.Steps, rev)
			result.Message = "No se aplicó: la VM no respondió como se esperaba con la configuración nueva y volvió a la del respaldo."
			if !ok {
				result.Message = fmt.Sprintf("Falló la verificación y no se pudo disparar la reversión: la VM volverá sola a la configuración anterior cuando venza el temporizador (%d s).", plan.RevertSeconds)
			}
		default:
			result.Message = "No se aplicó: falló el respaldo, así que no se cambió nada."
		}
	} else {
		result.OK = true
		result.Message = "Aplicado y verificado. Quedó en el archivo, así que sobrevive a un reinicio."
	}

	if fresh, err := readNetwork(p, name); err == nil {
		if mark != nil {
			mark(fresh)
		}
		result.State = fresh
	}
	return result, nil
}

// runPlanCmd ejecuta un comando del plan en la VM (por el agente) o en el
// hipervisor (por SSH) y decide si cumplio lo esperado.
func runPlanCmd(p netBackend, name string, c NetCmd) (ApplyLine, bool) {
	line := ApplyLine{Cmd: c.Cmd, Note: c.Note, Host: c.Host}
	run := c.run
	if run == "" {
		run = c.Cmd
	}
	attempts := c.retries + 1
	var ok bool
	for a := 0; a < attempts; a++ {
		if a > 0 {
			p.wait(time.Second)
		}
		var output string
		var code int
		if c.Host {
			out, err := p.runHost(run)
			output = string(out)
			if err != nil {
				code = 1
			}
		} else {
			res, err := p.GuestExec(name, run, guestStepTimeout)
			if err != nil {
				line.Output, line.Error = err.Error(), true
				return line, false
			}
			output, code = res.Output(), res.ExitCode
		}
		line.Output = strings.TrimRight(output, "\n")
		ok = true
		if c.mustOK && code != 0 {
			ok = false
		}
		if c.expect != "" && !strings.Contains(output, c.expect) {
			ok = false
		}
		if ok {
			break
		}
	}
	if !ok {
		if c.ignoreErr {
			// Un fallo esperado (ej. no habia reserva vieja que quitar) no se
			// muestra como error: se resume para que no asuste a quien lee.
			line.Output = "(no había nada que quitar; es normal)"
		} else if c.soft {
			line.Warn = true
		} else {
			line.Error = true
		}
	}
	return line, ok
}
