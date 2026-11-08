// /machines/:id/files — one computer's files.
import { Link, useParams } from 'react-router-dom'
import { FileBrowser } from '../components/FileBrowser'
import { Icon } from '../components/icons'
import { openEdit, useCrumb, useMachines } from '../components/Layout'
import { Empty, Loading } from '../components/ui'
import { kindIcon, statusLine } from '../lib/remote'

export default function Files() {
  const { id } = useParams()
  const { machines } = useMachines()
  const m = machines?.find(x => String(x.id) === id)
  useCrumb(m ? `Files on ${m.name}` : undefined)
  if (!machines) return <Loading />
  if (!m) return <Empty icon="monitor" title="No such computer"><Link to="/">Back to your computers</Link></Empty>
  return (
    <div className="page">
      <div className="page-head">
        <span className={`mc-ico k-${m.kind}`}><Icon name={kindIcon(m.kind)} size={22} /></span>
        <div className="grow"><h2>Files on {m.name}</h2><p className="muted small">{statusLine(m)} · over SFTP as {m.username || '—'}</p></div>
        <Link to={`/connect/${m.id}`} className="btn primary"><Icon name="log-in" size={15} />Connect</Link>
      </div>
      {!m.files_enabled
        ? <Empty icon="folder" title="Files are off for this computer">Switch on <b>Browse its files</b> in <button type="button" className="link-btn" onClick={() => openEdit(m)}>its settings</button>. A Mac needs Remote Login on; a Windows PC needs OpenSSH Server.</Empty>
        : <FileBrowser machine={m} />}
    </div>
  )
}
