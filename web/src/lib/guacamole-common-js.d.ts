// The parts of guacamole-common-js 1.5 Pocket Remote uses (the package ships no types).
declare module 'guacamole-common-js' {
  namespace Guacamole {
    interface Status { code: number; message?: string; isError(): boolean }
    interface MouseState { x: number; y: number; left: boolean; middle: boolean; right: boolean; up: boolean; down: boolean }
    interface MouseEvent { state: MouseState }
    class Display {
      getElement(): HTMLElement
      getWidth(): number
      getHeight(): number
      scale(s: number): void
      getScale(): number
      showCursor(shown: boolean): void
      onresize: ((w: number, h: number) => void) | null
      oncursor: ((canvas: HTMLCanvasElement, x: number, y: number) => void) | null
    }
    class Tunnel {
      onstatechange: ((s: number) => void) | null
      onerror: ((s: Status) => void) | null
    }
    class WebSocketTunnel extends Tunnel { constructor(url: string) }
    class InputStream { }
    class OutputStream { }
    class Client {
      constructor(t: Tunnel)
      getDisplay(): Display
      connect(data?: string): void
      disconnect(): void
      sendKeyEvent(pressed: 0 | 1, keysym: number): void
      sendMouseState(s: MouseState, applyDisplayScale?: boolean): void
      sendSize(w: number, h: number): void
      createClipboardStream(mimetype: string): OutputStream
      onstatechange: ((s: number) => void) | null
      onerror: ((s: Status) => void) | null
      onclipboard: ((s: InputStream, mimetype: string) => void) | null
      onname: ((n: string) => void) | null
    }
    class Mouse {
      constructor(el: Element)
      onEach(types: string[], fn: (e: MouseEvent) => void): void
    }
    namespace Mouse {
      class Touchpad { constructor(el: Element); onEach(types: string[], fn: (e: MouseEvent) => void): void }
      class Touchscreen { constructor(el: Element); onEach(types: string[], fn: (e: MouseEvent) => void): void }
    }
    class Keyboard {
      constructor(el?: Element | Document)
      onkeydown: ((keysym: number) => boolean | void) | null
      onkeyup: ((keysym: number) => void) | null
      listenTo(el: Element | Document): void
      reset(): void
    }
    class InputSink { getElement(): HTMLElement; focus(): void }
    class StringReader { constructor(s: InputStream); ontext: ((t: string) => void) | null; onend: (() => void) | null }
    class StringWriter { constructor(s: OutputStream); sendText(t: string): void; sendEnd(): void }
  }
  export default Guacamole
}
