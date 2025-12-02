" Craft code review vim integration
" Copy/link this file to your .vim/autoload/craft.vim
" Add these to .vimrc:
"nnoremap <leader>C :call craft#Comment()<CR>
"vnoremap <leader>C :call craft#Comment()<CR>

" Box drawing characters for craft comments
let s:box_thread = '╓'  " Start of new thread (header)
let s:box_reply = '╟'   " Reply within thread (header)
let s:box_body = '║'    " Body line

" Extract comment prefix from &commentstring (e.g., '// %s' -> '//')
function! craft#Prefix()
  let l:cs = &commentstring
  let l:idx = stridx(l:cs, '%s')
  if l:idx > 0
    return trim(l:cs[:l:idx-1])
  endif
  return '//'
endfunction

" Check if a line is a craft comment (contains any box char)
function! craft#IsCraftLine(lnum)
  let l:line = getline(a:lnum)
  return l:line =~# '[╓╟║]'
endfunction

" Set up buffer-local comments setting for craft line wrapping.
" This makes vim's formatoptions 'c' continue craft comments properly.
function! craft#SetupComments()
  " Check if already set up (idempotent)
  if &l:comments =~# '║'
    return
  endif

  " Parse existing comments setting
  let l:parts = split(&comments, ',')
  let l:new_parts = []

  for l:part in l:parts
    let l:colon = stridx(l:part, ':')
    if l:colon < 0
      continue
    endif
    let l:flags = l:colon > 0 ? l:part[:l:colon-1] : ''
    let l:chars = l:part[l:colon+1:]

    " Skip entries with f, s, m, or e flags (middle/end markers)
    if l:flags =~# '[fsme]'
      continue
    endif

    " Create craft version: original chars + ' ║' for body continuation
    call add(l:new_parts, l:flags . ':' . l:chars . ' ' . s:box_body)
  endfor

  " Prepend to comments (buffer-local)
  if len(l:new_parts) > 0
    let &l:comments = join(l:new_parts, ',') . ',' . &comments
  endif
endfunction

" Find the end of a craft comment chain (last consecutive craft line)
function! craft#IsChainEnd(lnum)
  let l:end = a:lnum
  while craft#IsCraftLine(l:end + 1)
    let l:end += 1
  endwhile
  return l:end
endfunction

" Get the indentation (leading whitespace) of a line
function! craft#GetIndent(lnum)
  let l:line = getline(a:lnum)
  return matchstr(l:line, '^\s*')
endfunction

" Main function: reply if in chain, otherwise new comment
function! craft#Comment() range
  let l:prefix = craft#Prefix()

  " Check if we're in a craft comment chain
  if craft#IsCraftLine(line('.'))
    " Reply: go to end of chain and add new comment with reply marker
    let l:insert_after = craft#IsChainEnd(line('.'))
    let l:indent = craft#GetIndent(line('.'))
    let l:header = l:indent . l:prefix . ' ' . s:box_reply . '───── new'
  elseif a:firstline != a:lastline
    " Visual range: add range comment after last line of selection
    let l:insert_after = a:lastline
    let l:indent = craft#GetIndent(a:lastline)
    let l:range_size = a:firstline - a:lastline
    let l:header = l:indent . l:prefix . ' ' . s:box_thread . '───── new ─ range ' . l:range_size
  else
    " New comment on current line
    let l:insert_after = line('.')
    let l:indent = craft#GetIndent(line('.'))
    let l:header = l:indent . l:prefix . ' ' . s:box_thread . '───── new'
  endif

  let l:body = l:indent . l:prefix . ' ' . s:box_body . ' '
  call append(l:insert_after, [l:header, l:body])
  call cursor(l:insert_after + 2, len(l:body) + 1)
  call craft#SetupComments()
  startinsert!
endfunction

