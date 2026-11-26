# guacd with a Nerd Font, so terminal sessions show the icons and prompt symbols
# of eza, starship, powerlevel10k and friends instead of empty boxes. guacd draws
# terminal text itself, with the fonts inside this image.
#
# guacd 1.6.0's terminal also printed the text of escape codes it does not know: zsh / oh-my-zsh set the
# tab name with ESC ] 1 ; <command> BEL before every command, and guacd showed "cloud" (gcloud minus its
# first letter) in front of the output. Newer guacd skips such codes; osc-unknown.patch backports that, so
# only the terminal library is rebuilt from the 1.6.0 source here and swapped into the stock image.
FROM alpine:3.18 AS terminal
ARG GUACD_VERSION=1.6.0
ARG GUACD_SHA256=8bc45675da96d7b6f39728160181e3d4ff3c08f460f6d26de5805b642bf13f2b
RUN apk add --no-cache build-base cairo-dev pango-dev libpng-dev libjpeg-turbo-dev libwebp-dev util-linux-dev curl patch
COPY osc-unknown.patch /tmp/osc-unknown.patch
RUN f="guacamole/${GUACD_VERSION}/source/guacamole-server-${GUACD_VERSION}.tar.gz" \
 && { curl -fsSL --retry 4 --retry-all-errors -o /tmp/gs.tgz "https://dlcdn.apache.org/$f" \
      || curl -fsSL --retry 4 --retry-all-errors -o /tmp/gs.tgz "https://archive.apache.org/dist/$f"; } \
 && echo "${GUACD_SHA256}  /tmp/gs.tgz" | sha256sum -c - \
 && mkdir /src && tar -xzf /tmp/gs.tgz -C /src --strip-components=1 \
 && cd /src && patch -p1 < /tmp/osc-unknown.patch \
 && ./configure --prefix=/opt/guacamole --disable-guacd --disable-guacenc --disable-guaclog \
      --without-rdp --without-vnc --without-ssh --without-telnet --without-kubernetes --without-pulse \
      --without-vorbis --without-libavcodec --without-libavformat --without-libavutil --without-libswscale \
      CPPFLAGS=-Wno-error=deprecated-declarations \
 && make -j"$(nproc)" -C src/libguac \
 && make -j"$(nproc)" -C src/common \
 && make -j"$(nproc)" -C src/terminal \
 && cp -L src/terminal/.libs/libguac-terminal.so.2 /libguac-terminal.so.2.0.0

FROM guacamole/guacd:1.6.0
USER root
COPY --from=terminal /libguac-terminal.so.2.0.0 /opt/guacamole/lib/libguac-terminal.so.2.0.0
ARG NERD_FONTS=v3.4.0
RUN apk add --no-cache --virtual .fetch curl xz \
 && mkdir -p /usr/share/fonts/nerd \
 && curl -fsSL "https://github.com/ryanoasis/nerd-fonts/releases/download/${NERD_FONTS}/JetBrainsMono.tar.xz" \
    | tar -xJ -C /usr/share/fonts/nerd 'JetBrainsMonoNerdFontMono-Regular.ttf' 'JetBrainsMonoNerdFontMono-Bold.ttf' 'JetBrainsMonoNerdFontMono-Italic.ttf' 'JetBrainsMonoNerdFontMono-BoldItalic.ttf' \
 && apk del .fetch \
 && fc-cache -f \
 && fc-list | grep -q 'JetBrainsMono Nerd Font Mono'
USER guacd
# guacd has no sign-in of its own, so it must only listen on 127.0.0.1 (host networking). The stock
# 1.6 entrypoint always adds -b 0.0.0.0 and swallows the compose command, so run guacd directly.
ENTRYPOINT []
CMD ["/opt/guacamole/sbin/guacd", "-b", "127.0.0.1", "-l", "4822", "-f", "-L", "info"]
