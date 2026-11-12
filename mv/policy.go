package vm

// ShareableDiskWarning documenta la politica de concurrencia para discos
// compartidos: la aplicacion NO implementa ningun bloqueo ni coordinacion
// entre VMs. El flag "shareable=on" que se pasa a libvirt solo desactiva el
// locking de imagen de QEMU (necesario para poder adjuntar el mismo disco a
// varias VMs), pero no evita que dos VMs escriban al mismo tiempo y corrompan
// los datos si el filesystem dentro del disco no esta preparado para acceso
// concurrente.
//
// Se opto deliberadamente por "permitir libremente, solo avisar" en vez de
// bloquear o mantener un lock a nivel de hipervisor: el caso de uso valido de
// un disco compartido (clusters con GFS2/OCFS2, coordinacion manual del
// usuario) requiere que varias VMs lo monten a la vez, asi que la aplicacion
// no puede asumir que "una sola VM a la vez" es la politica correcta. En su
// lugar, se advierte explicitamente en cada respuesta de la API que involucra
// un disco compartido para que quien lo use sea consciente del riesgo.
const ShareableDiskWarning = "Este disco es compartido (shareable): la aplicación no bloquea ni coordina el acceso entre VMs. Úsalo solo con un filesystem preparado para acceso concurrente (ej. GFS2, OCFS2) o coordinando manualmente qué VM escribe en cada momento; escribir desde varias VMs sin coordinación puede corromper los datos."

// HasShareableDisk indica si al menos un disco de la lista esta marcado como
// compartido, para decidir si corresponde emitir ShareableDiskWarning.
func HasShareableDisk(disks []DiskSpec) bool {
	for _, disk := range disks {
		if disk.Shareable {
			return true
		}
	}
	return false
}
