package transport

// Shutdown serializes with Restart and waits for the active generation. The
// transport chain calls it before trying another carrier on the same ports.
func (c *TcpTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}
func (c *TcpMuxTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}
func (c *KcpTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}
func (c *UdpTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}
func (c *WsTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}
func (c *WsMuxTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.StopAndWait()
}

func (c *QuicTransport) Shutdown() {
	c.restartMutex.Lock()
	defer c.restartMutex.Unlock()
	c.state.Stop()
	if qc := c.getQUICConn(); qc != nil {
		_ = qc.CloseWithError(0, "shutdown")
		c.setQUICConn(nil)
	}
	c.state.Wait()
}
