import httpClient from '../dataProvider/httpClient'
import { baseUrl } from '../utils'

const customAuthorizationHeader = 'X-ND-Authorization'

// The cover endpoints respond with a plain-text body on failure (e.g. "uploaded file
// is not an image"), not JSON, so httpClient can't parse it - surface that text as the
// error instead of a generic message, so the notification tells the user what to fix.
const rejectWithServerMessage = (response) => {
  if (response.ok) {
    return undefined
  }
  return response.text().then((text) => {
    throw new Error(text || 'Failed to upload cover art')
  })
}

// Cover art can't go through httpClient (it always parses the response as JSON),
// so this fetches the raw image bytes directly, reusing the same auth header, and
// hands back an object URL the caller must revoke once it's done with it.
const fetchCover = (jobId, trackId) => {
  const headers = new Headers()
  const token = localStorage.getItem('token')
  if (token) {
    headers.set(customAuthorizationHeader, `Bearer ${token}`)
  }
  return fetch(
    baseUrl(`/api/youtube-download/${jobId}/tracks/${trackId}/cover`),
    { headers },
  ).then((response) =>
    response.ok
      ? response.blob().then((blob) => URL.createObjectURL(blob))
      : null,
  )
}

// Same reasoning as fetchCover: httpClient always JSON-encodes the body, which would
// mangle raw image bytes, so this uploads directly with the same auth header.
const uploadCover = (jobId, trackId, file) => {
  const headers = new Headers({
    'Content-Type': file.type || 'application/octet-stream',
  })
  const token = localStorage.getItem('token')
  if (token) {
    headers.set(customAuthorizationHeader, `Bearer ${token}`)
  }
  return fetch(
    baseUrl(`/api/youtube-download/${jobId}/tracks/${trackId}/cover`),
    { method: 'PUT', headers, body: file },
  ).then((response) => rejectWithServerMessage(response))
}

// Same reasoning as uploadCover, but sets one cover shared by every track in the
// job at once (used once an album has been applied), rather than a single track's.
const uploadAlbumCover = (jobId, file) => {
  const headers = new Headers({
    'Content-Type': file.type || 'application/octet-stream',
  })
  const token = localStorage.getItem('token')
  if (token) {
    headers.set(customAuthorizationHeader, `Bearer ${token}`)
  }
  return fetch(baseUrl(`/api/youtube-download/${jobId}/cover`), {
    method: 'PUT',
    headers,
    body: file,
  }).then((response) => rejectWithServerMessage(response))
}

const start = (url) =>
  httpClient('/api/youtube-download', {
    method: 'POST',
    body: JSON.stringify({ url }),
  }).then((response) => response.json)

const getStatus = (jobId) =>
  httpClient(`/api/youtube-download/${jobId}`).then((response) => response.json)

const applyAlbum = (jobId, album, albumArtist) =>
  httpClient(`/api/youtube-download/${jobId}/album`, {
    method: 'POST',
    body: JSON.stringify({ album, albumArtist }),
  }).then((response) => response.json)

const confirm = (jobId, tracks) =>
  httpClient(`/api/youtube-download/${jobId}/confirm`, {
    method: 'POST',
    body: JSON.stringify({ tracks }),
  })

const reject = (jobId) =>
  httpClient(`/api/youtube-download/${jobId}`, { method: 'DELETE' })

export default {
  start,
  getStatus,
  applyAlbum,
  confirm,
  reject,
  fetchCover,
  uploadCover,
  uploadAlbumCover,
}
