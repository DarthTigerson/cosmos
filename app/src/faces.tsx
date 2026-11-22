import { useState, type FormEvent } from 'react'
import { api, useAsset, useLoad } from './api'
import { when } from './format'
import type { Face, Person, UnknownGroup } from './types'

// The faces she has seen: a picture of one, the question of who it is,
// and a row of someone's pictures. The pictures stay on this computer;
// the core serves them to this app only.

export function FacePicture({ id, size = 72, alt }: { id: number; size?: number; alt: string }) {
  const url = useAsset(`/api/images/${id}`)
  return <span className="face-pic" style={{ width: size, height: size }}>{url && <img src={url} alt={alt} width={size} height={size} />}</span>
}

// One-tap answers for someone new.
const kinds = ['son', 'daughter', 'partner', 'friend', 'family', 'colleague', 'guest']

// FaceAsk answers "who is this?" for a face she does not know (subject
// "faces:<group>"), or "was this them?" for one she half knew
// ("maybe:<face>").
export function FaceAsk({ subject, image, user, onDone }: { subject: string; image: number; user: string; onDone?: (said: string) => void }) {
  const [kind, id] = subject.split(':')
  const { data: people } = useLoad<Person[]>('/api/people')
  // Whether it is still a question: the group is still unknown, or the
  // face is still a maybe. An answer given before is not asked again.
  const { data: strangers } = useLoad<UnknownGroup[]>(kind === 'faces' ? '/api/faces/unknown' : '')
  const { data: face, error: gone } = useLoad<Face>(kind === 'maybe' ? `/api/faces/${id}` : '')
  const settled =
    kind === 'faces' ? !!strangers && !strangers.some((g) => g.id === Number(id)) : kind === 'maybe' ? !!gone || (!!face && face.status !== 'maybe') : false
  const [name, setName] = useState('')
  const [relationship, setRelationship] = useState('')
  const [done, setDone] = useState('')
  const [problem, setProblem] = useState('')
  const [picking, setPicking] = useState(false)

  const answer = (body: unknown, said: string) =>
    void api(kind === 'maybe' ? `/api/faces/maybe/${id}` : `/api/faces/groups/${id}`, { method: 'POST', body })
      .then(() => {
        setDone(said)
        onDone?.(said)
      })
      .catch((e: Error) => setProblem(e.message))
  const named = (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    answer({ name: name.trim(), relationship }, `That was ${name.trim()}. She will know them next time.`)
  }

  return (
    <div className="face-ask">
      <FacePicture id={image} size={96} alt="The face she is asking about" />
      <div className="face-answers">
        {done ? (
          <p className="muted">{done}</p>
        ) : settled ? (
          <p className="muted">Answered.</p>
        ) : kind === 'maybe' ? (
          <div className="quick">
            <button type="button" onClick={() => answer({ yes: true }, 'Thanks. She is surer now.')}>
              yes
            </button>
            <button type="button" onClick={() => answer({ yes: false }, 'Not them. She will ask about them as someone new.')}>
              no
            </button>
          </div>
        ) : (
          <>
            <form className="face-name" onSubmit={named}>
              <input placeholder="Their name" aria-label="Their name" value={name} onChange={(e) => setName(e.target.value)} />
              <button type="submit" className="secondary" disabled={!name.trim()}>
                That's them
              </button>
            </form>
            {name.trim() && (
              <div className="quick" aria-label="Who they are to you">
                {kinds.map((k) => (
                  <button key={k} type="button" aria-pressed={relationship === k} onClick={() => setRelationship(relationship === k ? '' : k)}>
                    {k}
                  </button>
                ))}
              </div>
            )}
            <div className="quick">
              <button type="button" onClick={() => answer({ person_id: -1 }, `That was you, ${user}.`)}>
                that's me
              </button>
              <button type="button" aria-pressed={picking} onClick={() => setPicking(!picking)}>
                someone she knows…
              </button>
              <button type="button" onClick={() => answer({ forget: true }, 'Forgotten. The pictures are deleted.')}>
                don't remember them
              </button>
            </div>
            {picking && (
              <div className="quick">
                {(people ?? []).map((p) => (
                  <button key={p.id} type="button" onClick={() => answer({ person_id: p.id }, `That was ${p.name}.`)}>
                    {p.name}
                  </button>
                ))}
              </div>
            )}
          </>
        )}
        {problem && <p className="problem-inline">{problem}</p>}
      </div>
    </div>
  )
}

// Faces is a row of someone's pictures; each can be let go.
export function Faces({ faces, name, onForget }: { faces: Face[]; name: string; onForget: (id: number) => void }) {
  if (faces.length === 0) return null
  return (
    <ul className="face-row" aria-label={`Pictures of ${name}`}>
      {faces.map((f) => (
        <li key={f.id} title={`${when(f.at)}${f.status === 'maybe' ? ' · she was not sure' : ''}`}>
          <FacePicture id={f.image_id} size={56} alt={`${name}, ${when(f.at)}`} />
          <button type="button" className="face-forget" aria-label="Forget this picture" onClick={() => onForget(f.id)}>
            ×
          </button>
        </li>
      ))}
    </ul>
  )
}
