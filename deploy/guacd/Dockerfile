# guacd with a Nerd Font, so terminal sessions show the icons and prompt symbols
# of eza, starship, powerlevel10k and friends instead of empty boxes. guacd draws
# terminal text itself, with the fonts inside this image.
FROM guacamole/guacd:1.5.5
USER root
ARG NERD_FONTS=v3.4.0
RUN apk add --no-cache --virtual .fetch curl xz \
 && mkdir -p /usr/share/fonts/nerd \
 && curl -fsSL "https://github.com/ryanoasis/nerd-fonts/releases/download/${NERD_FONTS}/JetBrainsMono.tar.xz" \
    | tar -xJ -C /usr/share/fonts/nerd 'JetBrainsMonoNerdFontMono-Regular.ttf' 'JetBrainsMonoNerdFontMono-Bold.ttf' 'JetBrainsMonoNerdFontMono-Italic.ttf' 'JetBrainsMonoNerdFontMono-BoldItalic.ttf' \
 && apk del .fetch \
 && fc-cache -f \
 && fc-list | grep -q 'JetBrainsMono Nerd Font Mono'
USER guacd
