import React, { useState } from 'react'
import DeleteIcon from '@material-ui/icons/Delete'
import { makeStyles, alpha } from '@material-ui/core/styles'
import clsx from 'clsx'
import {
  Button,
  Confirm,
  useNotify,
  useDeleteMany,
  useRefresh,
  useUnselectAll,
  useTranslate,
} from 'react-admin'

const useStyles = makeStyles(
  (theme) => ({
    deleteButton: {
      color: theme.palette.error.main,
      '&:hover': {
        backgroundColor: alpha(theme.palette.error.main, 0.12),
        '@media (hover: none)': {
          backgroundColor: 'transparent',
        },
      },
    },
  }),
  { name: 'RaDeleteWithConfirmButton' },
)

const DeleteSongsButton = (props) => {
  const { selectedIds, className } = props
  const [open, setOpen] = useState(false)
  const unselectAll = useUnselectAll()
  const refresh = useRefresh()
  const notify = useNotify()
  const translate = useTranslate()

  const [deleteMany, { loading }] = useDeleteMany('song', selectedIds, {
    onSuccess: () => {
      notify('resources.song.notifications.deleted', 'info', {
        smart_count: selectedIds.length,
      })
      refresh()
      unselectAll('song')
    },
    onFailure: (error) =>
      notify(error?.message || 'resources.song.notifications.deleteError', {
        type: 'warning',
      }),
  })
  const handleClick = () => setOpen(true)
  const handleDialogClose = () => setOpen(false)
  const handleConfirm = () => {
    deleteMany()
    setOpen(false)
  }

  const classes = useStyles(props)

  return (
    <>
      <Button
        onClick={handleClick}
        label={translate('ra.action.delete')}
        key="button"
        className={clsx('ra-delete-button', classes.deleteButton, className)}
      >
        <DeleteIcon />
      </Button>
      <Confirm
        isOpen={open}
        loading={loading}
        title={translate('resources.song.name', {
          smart_count: selectedIds.length,
        })}
        content={translate('resources.song.messages.deleteConfirm', {
          smart_count: selectedIds.length,
        })}
        onConfirm={handleConfirm}
        onClose={handleDialogClose}
      />
    </>
  )
}

export default DeleteSongsButton
