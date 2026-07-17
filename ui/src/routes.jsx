import React from 'react'
import { Route } from 'react-router-dom'
import Personal from './personal/Personal'
import YoutubeDownloadPage from './youtube/YoutubeDownloadPage'

const routes = [
  <Route exact path="/personal" render={() => <Personal />} key={'personal'} />,
  <Route
    exact
    path="/youtube-download"
    render={() => <YoutubeDownloadPage />}
    key={'youtube-download'}
  />,
]

export default routes
