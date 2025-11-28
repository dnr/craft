" Craft code review vim integration
" Add to your .vimrc:  source /path/to/craft/vim_craft.vim

" Extract comment prefix from &commentstring (e.g., '// %s' -> '//')
function! CraftPrefix()
  let l:cs = &commentstring
  let l:idx = stridx(l:cs, '%s')
  if l:idx > 0
    return trim(l:cs[:l:idx-1])
  endif
  return '//'
endfunction

" Check if a line is a craft comment
function! CraftIsCraftLine(lnum)
  return getline(a:lnum) =~# ' ❯ '
endfunction

" Find the end of a craft comment chain (last consecutive craft line)
function! CraftChainEnd(lnum)
  let l:end = a:lnum
  while CraftIsCraftLine(l:end + 1)
    let l:end += 1
  endwhile
  return l:end
endfunction

" Main function: reply if in chain, otherwise new comment
function! CraftComment() range
  let l:prefix = CraftPrefix()

  " Check if we're in a craft comment chain
  if CraftIsCraftLine(line('.'))
    " Reply: go to end of chain and add new comment
    let l:insert_after = CraftChainEnd(line('.'))
    let l:header = l:prefix . ' ❯ ───── new ─────'
  elseif a:firstline != a:lastline
    " Visual range: add range comment
    let l:insert_after = a:lastline
    let l:range_size = a:firstline - a:lastline
    let l:header = l:prefix . ' ❯ ───── new ─ range ' . l:range_size . ' ─────'
  else
    " New comment on current line
    let l:insert_after = line('.')
    let l:header = l:prefix . ' ❯ ───── new ─────'
  endif

  let l:body = l:prefix . ' ❯ '
  call append(l:insert_after, [l:header, l:body])
  call cursor(l:insert_after + 2, len(l:body) + 1)
  startinsert!
endfunction

nnoremap <leader>cc :call CraftComment()<CR>
vnoremap <leader>cc :call CraftComment()<CR>
