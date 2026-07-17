import React, { useEffect, useRef, useState } from 'react'
import { Title, useNotify, useTranslate } from 'react-admin'
import {
  Button,
  Card,
  CardContent,
  Checkbox,
  CircularProgress,
  FormControlLabel,
  IconButton,
  Paper,
  TextField,
  Typography,
} from '@material-ui/core'
import { makeStyles, alpha } from '@material-ui/core/styles'
import EditIcon from '@material-ui/icons/Edit'
import { useInterval } from '../common/useInterval'
import youtubeApi from './youtubeApi'
import AlbumCandidates from './AlbumCandidates'

const useStyles = makeStyles((theme) => ({
  root: { marginTop: '1em', maxWidth: 1400 },
  form: {
    display: 'flex',
    gap: theme.spacing(2),
    alignItems: 'flex-start',
  },
  urlField: { flex: 1 },
  status: {
    display: 'flex',
    alignItems: 'center',
    gap: theme.spacing(2),
    marginTop: theme.spacing(2),
  },
  field: { marginTop: theme.spacing(2) },
  header: {
    display: 'flex',
    gap: theme.spacing(4),
    alignItems: 'flex-start',
  },
  headerMain: { flex: 1, minWidth: 0 },
  albumCoverColumn: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: theme.spacing(1),
    flexShrink: 0,
    width: 220,
    textAlign: 'center',
  },
  albumCoverWrapper: {
    position: 'relative',
    width: 220,
    height: 220,
  },
  albumCover: {
    width: 220,
    height: 220,
    objectFit: 'cover',
    borderRadius: 4,
  },
  albumCoverPlaceholder: {
    width: 220,
    height: 220,
    borderRadius: 4,
    border: `1px dashed ${theme.palette.divider}`,
  },
  albumCoverEditButton: {
    position: 'absolute',
    top: theme.spacing(1),
    left: theme.spacing(1),
    backgroundColor: alpha(theme.palette.background.paper, 0.8),
    '&:hover': {
      backgroundColor: alpha(theme.palette.background.paper, 0.95),
    },
  },
  trackGrid: {
    display: 'grid',
    gridTemplateColumns: '1fr',
    gap: theme.spacing(3),
    marginTop: theme.spacing(3),
    [theme.breakpoints.up('lg')]: {
      gridTemplateColumns: '1fr 1fr',
    },
  },
  trackCard: { padding: theme.spacing(2) },
  trackHeader: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  trackBody: {
    display: 'flex',
    gap: theme.spacing(2),
  },
  coverColumn: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: theme.spacing(1),
    marginTop: theme.spacing(2),
    flexShrink: 0,
  },
  cover: {
    width: 96,
    height: 96,
    objectFit: 'cover',
    borderRadius: 4,
  },
  coverPlaceholder: {
    width: 96,
    height: 96,
    borderRadius: 4,
    border: `1px dashed ${theme.palette.divider}`,
  },
  hiddenInput: { display: 'none' },
  trackFields: { flex: 1 },
  actions: {
    display: 'flex',
    gap: theme.spacing(2),
    marginTop: theme.spacing(3),
  },
  error: { marginTop: theme.spacing(2) },
}))

const POLL_INTERVAL_MS = 2000

const emptyTags = {
  title: '',
  artist: '',
  album: '',
  albumArtist: '',
  genre: '',
  year: '',
  trackNumber: '',
}

const tracksFromJob = (job) =>
  (job.tracks || []).map((t) => ({
    id: t.id,
    error: t.error,
    include: true,
    tags: { ...emptyTags, ...t.tags },
  }))

const YoutubeDownloadPage = () => {
  const classes = useStyles()
  const translate = useTranslate()
  const notify = useNotify()

  const [url, setUrl] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [job, setJob] = useState(null)
  const [tracks, setTracks] = useState([])
  const [saving, setSaving] = useState(false)
  const [covers, setCovers] = useState({})
  const coversRef = useRef(covers)
  coversRef.current = covers
  // Separate from per-track `covers`: once an album is applied, every track shares
  // this one embedded cover instead of each having its own.
  const [albumCoverUrl, setAlbumCoverUrl] = useState(null)
  const albumCoverUrlRef = useRef(albumCoverUrl)
  albumCoverUrlRef.current = albumCoverUrl

  const isActive =
    job && (job.state === 'downloading' || job.state === 'tagging')

  useInterval(
    () => {
      youtubeApi
        .getStatus(job.id)
        .then((updated) => {
          setJob(updated)
          if (updated.state === 'awaiting_review') {
            setTracks(tracksFromJob(updated))
            ;(updated.tracks || []).forEach((t) => {
              youtubeApi.fetchCover(updated.id, t.id).then((coverUrl) => {
                if (coverUrl) {
                  setCovers((current) => ({ ...current, [t.id]: coverUrl }))
                }
              })
            })
          }
        })
        .catch(() => {
          notify('youtube.notifications.statusError', {
            type: 'warning',
          })
        })
    },
    isActive ? POLL_INTERVAL_MS : null,
  )

  // Object URLs are only valid client-side and must be released explicitly, or
  // the blobs they point at leak for the lifetime of the tab.
  useEffect(() => {
    return () => {
      Object.values(coversRef.current).forEach((url) =>
        URL.revokeObjectURL(url),
      )
      if (albumCoverUrlRef.current) {
        URL.revokeObjectURL(albumCoverUrlRef.current)
      }
    }
  }, [])

  const handleDownload = () => {
    if (!url.trim()) {
      return
    }
    setSubmitting(true)
    youtubeApi
      .start(url.trim())
      .then((newJob) => {
        setJob(newJob)
        setUrl('')
      })
      .catch((error) => {
        notify(error.message || 'youtube.notifications.startError', {
          type: 'warning',
        })
      })
      .finally(() => setSubmitting(false))
  }

  const reset = () => {
    setJob(null)
    setTracks([])
    Object.values(coversRef.current).forEach((url) => URL.revokeObjectURL(url))
    setCovers({})
    if (albumCoverUrlRef.current) {
      URL.revokeObjectURL(albumCoverUrlRef.current)
    }
    setAlbumCoverUrl(null)
  }

  const handleApplyAlbum = (album, albumArtist, coverFile) =>
    youtubeApi
      .applyAlbum(job.id, album, albumArtist)
      .then((updatedJob) => {
        setJob(updatedJob)
        setTracks(tracksFromJob(updatedJob))
        notify('youtube.notifications.albumApplied', { type: 'info' })

        if (coverFile) {
          // A manually-supplied cover always wins over whatever MusicBrainz found -
          // show it immediately and upload it in place of fetching Cover Art Archive.
          if (albumCoverUrlRef.current) {
            URL.revokeObjectURL(albumCoverUrlRef.current)
          }
          setAlbumCoverUrl(URL.createObjectURL(coverFile))
          youtubeApi.uploadAlbumCover(job.id, coverFile).catch((error) => {
            notify(error.message || 'youtube.notifications.coverUploadError', {
              type: 'warning',
            })
          })
          return
        }

        const firstTrack = (updatedJob.tracks || [])[0]
        if (updatedJob.appliedAlbum && firstTrack) {
          youtubeApi
            .fetchCover(updatedJob.id, firstTrack.id)
            .then((coverUrl) => {
              if (albumCoverUrlRef.current) {
                URL.revokeObjectURL(albumCoverUrlRef.current)
              }
              setAlbumCoverUrl(coverUrl)
            })
        }
      })
      .catch((error) => {
        notify(error.message || 'youtube.notifications.albumApplyError', {
          type: 'warning',
        })
      })

  const handleConfirm = () => {
    setSaving(true)
    youtubeApi
      .confirm(
        job.id,
        tracks.map(({ id, include, tags }) => ({ id, include, tags })),
      )
      .then(() => {
        notify('youtube.notifications.confirmed', {
          type: 'info',
        })
        reset()
      })
      .catch((error) => {
        notify(error.message || 'youtube.notifications.confirmError', {
          type: 'warning',
        })
      })
      .finally(() => setSaving(false))
  }

  const handleDiscard = () => {
    setSaving(true)
    youtubeApi
      .reject(job.id)
      .then(() => {
        notify('youtube.notifications.rejected', { type: 'info' })
        reset()
      })
      .catch(() => {
        reset()
      })
      .finally(() => setSaving(false))
  }

  const handleTagChange = (trackId, field) => (e) => {
    setTracks((current) =>
      current.map((t) =>
        t.id === trackId
          ? { ...t, tags: { ...t.tags, [field]: e.target.value } }
          : t,
      ),
    )
  }

  const handleIncludeChange = (trackId) => (e) => {
    setTracks((current) =>
      current.map((t) =>
        t.id === trackId ? { ...t, include: e.target.checked } : t,
      ),
    )
  }

  const handleCoverUpload = (trackId) => (e) => {
    const file = e.target.files && e.target.files[0]
    // Allow re-selecting the same file (e.g. after fixing a bad crop) later.
    e.target.value = ''
    if (!file) {
      return
    }
    // Show the picked image immediately rather than waiting on the upload
    // round-trip; if the upload fails, the notification tells the user to retry.
    const localUrl = URL.createObjectURL(file)
    setCovers((current) => {
      if (current[trackId]) {
        URL.revokeObjectURL(current[trackId])
      }
      return { ...current, [trackId]: localUrl }
    })
    youtubeApi.uploadCover(job.id, trackId, file).catch((error) => {
      notify(error.message || 'youtube.notifications.coverUploadError', {
        type: 'warning',
      })
    })
  }

  const handleAlbumCoverUpload = (e) => {
    const file = e.target.files && e.target.files[0]
    e.target.value = ''
    if (!file) {
      return
    }
    const localUrl = URL.createObjectURL(file)
    if (albumCoverUrlRef.current) {
      URL.revokeObjectURL(albumCoverUrlRef.current)
    }
    setAlbumCoverUrl(localUrl)
    youtubeApi.uploadAlbumCover(job.id, file).catch((error) => {
      notify(error.message || 'youtube.notifications.coverUploadError', {
        type: 'warning',
      })
    })
  }

  const anyIncluded = tracks.some((t) => t.include)

  return (
    <Card className={classes.root}>
      <Title title={'Navidrome - ' + translate('youtube.pageTitle')} />
      <CardContent>
        {!job && (
          <div className={classes.form}>
            <TextField
              className={classes.urlField}
              label={translate('youtube.urlLabel')}
              placeholder={translate('youtube.urlPlaceholder')}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              variant="outlined"
              disabled={submitting}
              fullWidth
            />
            <Button
              variant="contained"
              color="primary"
              onClick={handleDownload}
              disabled={submitting || !url.trim()}
            >
              {translate('youtube.downloadButton')}
            </Button>
          </div>
        )}

        {isActive && (
          <div className={classes.status}>
            <CircularProgress size={24} />
            <Typography variant="body1">
              {translate(`youtube.status.${job.state}`)}
            </Typography>
          </div>
        )}

        {job && job.state === 'failed' && (
          <div className={classes.error}>
            <Typography variant="body1" color="error">
              {translate('youtube.status.failed')}
              {job.error ? `: ${job.error}` : ''}
            </Typography>
            <div className={classes.actions}>
              <Button variant="outlined" onClick={reset}>
                {translate('youtube.tryAgainButton')}
              </Button>
            </div>
          </div>
        )}

        {job && job.state === 'awaiting_review' && (
          <div>
            <div className={classes.header}>
              <div className={classes.headerMain}>
                <Typography variant="body1" className={classes.field}>
                  {translate('youtube.reviewTitle')}
                </Typography>
                <AlbumCandidates
                  candidates={job.albumCandidates || []}
                  disabled={saving}
                  onApply={handleApplyAlbum}
                />
              </div>
              {job.appliedAlbum && (
                <div className={classes.albumCoverColumn}>
                  <div className={classes.albumCoverWrapper}>
                    {albumCoverUrl ? (
                      <img
                        className={classes.albumCover}
                        src={albumCoverUrl}
                        alt={translate('youtube.coverAltLabel')}
                      />
                    ) : (
                      <div className={classes.albumCoverPlaceholder} />
                    )}
                    <input
                      id="youtube-album-cover-upload"
                      className={classes.hiddenInput}
                      type="file"
                      accept="image/*"
                      onChange={handleAlbumCoverUpload}
                      disabled={saving}
                    />
                    <label htmlFor="youtube-album-cover-upload">
                      <IconButton
                        component="span"
                        size="small"
                        className={classes.albumCoverEditButton}
                        disabled={saving}
                        title={translate(
                          albumCoverUrl
                            ? 'youtube.changeCoverButton'
                            : 'youtube.addCoverButton',
                        )}
                        aria-label={translate(
                          albumCoverUrl
                            ? 'youtube.changeCoverButton'
                            : 'youtube.addCoverButton',
                        )}
                      >
                        <EditIcon fontSize="small" />
                      </IconButton>
                    </label>
                  </div>
                  <Typography variant="body2">
                    {job.appliedAlbum.album}
                  </Typography>
                  <Typography variant="caption" color="textSecondary">
                    {job.appliedAlbum.albumArtist}
                  </Typography>
                </div>
              )}
            </div>
            <div className={classes.trackGrid}>
              {tracks.map((track) => (
                <Paper
                  key={track.id}
                  variant="outlined"
                  className={classes.trackCard}
                >
                  <div className={classes.trackHeader}>
                    <FormControlLabel
                      control={
                        <Checkbox
                          checked={track.include}
                          onChange={handleIncludeChange(track.id)}
                          disabled={saving}
                          color="primary"
                        />
                      }
                      label={translate('youtube.includeLabel')}
                    />
                  </div>
                  {track.error && (
                    <Typography variant="body2" color="error">
                      {translate('youtube.trackErrorLabel')}: {track.error}
                    </Typography>
                  )}
                  <div className={classes.trackBody}>
                    {!job.appliedAlbum && (
                      <div className={classes.coverColumn}>
                        {covers[track.id] ? (
                          <img
                            className={classes.cover}
                            src={covers[track.id]}
                            alt={translate('youtube.coverAltLabel')}
                          />
                        ) : (
                          <div className={classes.coverPlaceholder} />
                        )}
                        <input
                          id={`youtube-cover-upload-${track.id}`}
                          className={classes.hiddenInput}
                          type="file"
                          accept="image/*"
                          onChange={handleCoverUpload(track.id)}
                          disabled={saving || !track.include}
                        />
                        <label htmlFor={`youtube-cover-upload-${track.id}`}>
                          <Button
                            component="span"
                            size="small"
                            variant="outlined"
                            disabled={saving || !track.include}
                          >
                            {translate(
                              covers[track.id]
                                ? 'youtube.changeCoverButton'
                                : 'youtube.addCoverButton',
                            )}
                          </Button>
                        </label>
                      </div>
                    )}
                    <div className={classes.trackFields}>
                      {Object.keys(emptyTags).map((field) => (
                        <TextField
                          key={field}
                          className={classes.field}
                          label={translate(`youtube.fields.${field}`)}
                          value={track.tags[field]}
                          onChange={handleTagChange(track.id, field)}
                          variant="outlined"
                          fullWidth
                          disabled={saving || !track.include}
                        />
                      ))}
                    </div>
                  </div>
                </Paper>
              ))}
            </div>
            <div className={classes.actions}>
              <Button
                variant="contained"
                color="primary"
                onClick={handleConfirm}
                disabled={saving || !anyIncluded}
              >
                {translate('youtube.confirmButton')}
              </Button>
              <Button
                variant="outlined"
                onClick={handleDiscard}
                disabled={saving}
              >
                {translate('youtube.discardButton')}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export default YoutubeDownloadPage
