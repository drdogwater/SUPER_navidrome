import React from 'react'
import DeleteIcon from '@material-ui/icons/Delete'
import { makeStyles, alpha } from '@material-ui/core/styles'
import clsx from 'clsx'
import {
  useNotify,
  useDeleteWithConfirmController,
  Button,
  Confirm,
  useTranslate,
  useRedirect,
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

const DeleteAlbumButton = ({ record, className, ...props }) => {
  const translate = useTranslate()
  const notify = useNotify()
  const redirect = useRedirect()

  const onSuccess = () => {
    notify('resources.album.notifications.deleted', 'info', {
      smart_count: 1,
    })
    redirect('/album')
  }

  const { open, loading, handleDialogOpen, handleDialogClose, handleDelete } =
    useDeleteWithConfirmController({
      resource: 'album',
      record,
      basePath: '/album',
      onSuccess,
      onFailure: (error) =>
        notify(error?.message || 'resources.album.notifications.deleteError', {
          type: 'warning',
        }),
    })

  const classes = useStyles(props)
  return (
    <>
      <Button
        label={translate('ra.action.delete')}
        onClick={handleDialogOpen}
        disabled={loading}
        className={clsx('ra-delete-button', classes.deleteButton, className)}
      >
        <DeleteIcon />
      </Button>
      <Confirm
        isOpen={open}
        loading={loading}
        title={translate('resources.album.name', { smart_count: 1 })}
        content={translate('resources.album.messages.deleteConfirm')}
        onConfirm={handleDelete}
        onClose={handleDialogClose}
      />
    </>
  )
}

export default DeleteAlbumButton
