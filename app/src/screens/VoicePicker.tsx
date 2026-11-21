import { useRef, useState } from 'react'
import { api, useLoad } from '../api'
import { Problem } from '../parts'

interface VoicesView {
  voices: { id: string; label: string }[]
  current: string
  missing: string
}

const sample = "Morning. You slept badly, didn't you? The coffee will help. I put the dentist in for Tuesday at half past ten."

// The sound of her voice: listen to each, keep the one you like.
export function VoicePicker() {
  const { data, error, reload } = useLoad<VoicesView>('/api/voices')
  const [problem, setProblem] = useState('')
  const [playing, setPlaying] = useState('')
  const audio = useRef<HTMLAudioElement | null>(null)

  async function hear(id: string) {
    setProblem('')
    audio.current?.pause()
    setPlaying(id)
    try {
      const res = await api<{ audio: string }>('/api/speech', { method: 'POST', body: { text: sample, voice: id } })
      const wav = Uint8Array.from(atob(res.audio), (c) => c.charCodeAt(0))
      const el = new Audio(URL.createObjectURL(new Blob([wav], { type: 'audio/wav' })))
      audio.current = el
      el.onended = () => setPlaying('')
      await el.play()
    } catch (e) {
      setProblem((e as Error).message)
      setPlaying('')
    }
  }

  async function choose(id: string) {
    setProblem('')
    try {
      await api('/api/voices', { method: 'PUT', body: { voice: id } })
      reload()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }

  return (
    <section className="settings">
      <fieldset>
        <legend>The sound of her voice</legend>
        <p className="note">Used when you talk to her. It is made on this computer; nothing is sent anywhere.</p>
        <Problem message={error || problem} />
        {data?.missing && <Problem message={`Her own voice is not ready (${data.missing}), so she uses the computer's built-in one.`} />}
        <ul className="voices" role="radiogroup" aria-label="Her voice">
          {(data?.voices ?? []).map((v) => (
            <li key={v.id}>
              <button type="button" role="radio" aria-checked={data?.current === v.id} className="file" onClick={() => void choose(v.id)}>
                <i aria-hidden="true" />
                <span className="file-name">{v.label}</span>
              </button>
              <button type="button" className="secondary" disabled={!!data?.missing} onClick={() => void hear(v.id)}>
                {playing === v.id ? 'Playing…' : 'Hear it'}
              </button>
            </li>
          ))}
        </ul>
      </fieldset>
    </section>
  )
}
